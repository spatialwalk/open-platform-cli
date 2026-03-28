#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
proto_root="${1:-${SHARED_PROTO_ROOT:-"$repo_root/../shared-proto"}}"
tmpdir=""

cleanup() {
  if [ -n "$tmpdir" ] && [ -d "$tmpdir" ]; then
    rm -rf "$tmpdir"
  fi
}

trap cleanup EXIT

for dir in common console jsonapi; do
  if [ ! -d "$proto_root/$dir" ]; then
    echo "missing protobuf source directory: $proto_root/$dir" >&2
    exit 1
  fi
done

mkdir -p "$repo_root/api/generated"

tmpdir="$(mktemp -d "$repo_root/.protobuf-codegen.XXXXXX")"

cp "$repo_root/buf.yaml" "$tmpdir/buf.yaml"
if [ -f "$repo_root/buf.lock" ]; then
  cp "$repo_root/buf.lock" "$tmpdir/buf.lock"
fi

for dir in common console jsonapi; do
  cp -R "$proto_root/$dir" "$tmpdir/$dir"
done

sed 's#out: api/generated#out: out#' "$repo_root/buf.gen.yaml" > "$tmpdir/buf.gen.yaml"

(
  cd "$tmpdir"
  buf dep update
  buf generate . --template buf.gen.yaml
)

for dir in buf common console google jsonapi; do
  rm -rf "$repo_root/api/generated/$dir"
done

cp -R "$tmpdir/out/." "$repo_root/api/generated"
cp "$tmpdir/buf.lock" "$repo_root/buf.lock"
