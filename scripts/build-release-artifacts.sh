#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 || $# -gt 2 ]]; then
  echo "usage: $0 <tag-name> [output-dir]" >&2
  exit 1
fi

tag_name="$1"
output_dir="${2:-dist}"
cli_name="${CLI_NAME:-avtkit}"
version="${tag_name#v}"
build_date="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
commit="${COMMIT:-$(git rev-parse --short=12 HEAD)}"

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"

if [[ "$output_dir" != /* ]]; then
  output_dir="$repo_root/$output_dir"
fi

mkdir -p "$output_dir"

ldflags="-s -w -X github.com/spatialwalk/open-platform-cli/internal/avtkitcli.version=${version} -X github.com/spatialwalk/open-platform-cli/internal/avtkitcli.commit=${commit} -X github.com/spatialwalk/open-platform-cli/internal/avtkitcli.buildDate=${build_date}"

targets=(
  "darwin amd64"
  "darwin arm64"
  "linux amd64"
  "linux arm64"
  "windows amd64"
)

workdir="$(mktemp -d)"
cleanup() {
  rm -rf "$workdir"
}
trap cleanup EXIT

for target in "${targets[@]}"; do
  read -r goos goarch <<<"$target"

  stage_dir="$workdir/${goos}_${goarch}"
  mkdir -p "$stage_dir"

  binary_name="$cli_name"
  archive_ext=".tar.gz"
  if [[ "$goos" == "windows" ]]; then
    binary_name="${cli_name}.exe"
    archive_ext=".zip"
  fi

  asset_name="${cli_name}_${tag_name}_${goos}_${goarch}${archive_ext}"
  binary_path="$stage_dir/$binary_name"

  (
    cd "$repo_root"
    GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go build -trimpath -ldflags "$ldflags" -o "$binary_path" ./cmd/avtkit
  )

  if [[ "$goos" == "windows" ]]; then
    (
      cd "$stage_dir"
      zip -q -9 "$output_dir/$asset_name" "$binary_name"
    )
  else
    tar -C "$stage_dir" -czf "$output_dir/$asset_name" "$binary_name"
  fi

  echo "built $asset_name"
done
