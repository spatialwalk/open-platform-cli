function shellQuote(value) {
  return `'${String(value).replaceAll("'", "'\"'\"'")}'`;
}

export function renderBootstrapScript({
  baseUrl,
  cliName,
  defaultInstallDir = "/usr/local/bin",
  mode,
}) {
  return `#!/bin/sh
set -eu

CLI_NAME=${shellQuote(cliName)}
MODE=${shellQuote(mode)}
BASE_URL=${shellQuote(baseUrl)}
DEFAULT_INSTALL_DIR=${shellQuote(defaultInstallDir)}
DOWNLOAD_URL="$BASE_URL/releases/latest/download"

log() {
  printf '%s\\n' "$*" >&2
}

fail() {
  log "error: $*"
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

detect_os() {
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$os" in
    darwin)
      printf 'darwin\\n'
      ;;
    linux)
      printf 'linux\\n'
      ;;
    *)
      fail "unsupported operating system: $os"
      ;;
  esac
}

detect_arch() {
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64)
      printf 'amd64\\n'
      ;;
    arm64|aarch64)
      printf 'arm64\\n'
      ;;
    *)
      fail "unsupported architecture: $arch"
      ;;
  esac
}

resolve_install_dir() {
  if [ -n "\${AVTKIT_INSTALL_DIR:-}" ]; then
    printf '%s\\n' "$AVTKIT_INSTALL_DIR"
    return
  fi

  if [ -w "$DEFAULT_INSTALL_DIR" ]; then
    printf '%s\\n' "$DEFAULT_INSTALL_DIR"
    return
  fi

  printf '%s/.local/bin\\n' "$HOME"
}

copy_binary() {
  src="$1"
  dst="$2"
  if [ -w "$(dirname "$dst")" ]; then
    cp "$src" "$dst"
    chmod 0755 "$dst"
    return
  fi

  command -v sudo >/dev/null 2>&1 || fail "destination requires elevated privileges; install sudo or set AVTKIT_INSTALL_DIR"
  sudo cp "$src" "$dst"
  sudo chmod 0755 "$dst"
}

abort_existing_install() {
  if [ "$MODE" != "install" ] || [ ! -e "$target_path" ]; then
    return
  fi

  log "error: found an existing $CLI_NAME binary at $target_path"
  log "error: install.sh only supports fresh installs and will not overwrite an existing binary"
  log "error: to replace it, run the upgrade script instead: curl -fsSL $BASE_URL/upgrade.sh | sh"
  exit 1
}

need_cmd curl
need_cmd tar
need_cmd uname
need_cmd mktemp
need_cmd find
need_cmd head

os="$(detect_os)"
arch="$(detect_arch)"
install_dir="$(resolve_install_dir)"

workdir="$(mktemp -d)"
cleanup() {
  rm -rf "$workdir"
}
trap cleanup EXIT INT TERM

archive_path="$workdir/$CLI_NAME.tar.gz"
binary_path="$workdir/$CLI_NAME"
target_path="$install_dir/$CLI_NAME"

mkdir -p "$install_dir"
abort_existing_install

log "$MODE $CLI_NAME for $os/$arch"
curl -fsSL "$DOWNLOAD_URL/$os/$arch" -o "$archive_path"

tar -xzf "$archive_path" -C "$workdir"

if [ ! -x "$binary_path" ]; then
  binary_path="$(find "$workdir" -type f -name "$CLI_NAME" | head -n 1)"
fi

[ -n "$binary_path" ] || fail "release archive did not contain $CLI_NAME"

copy_binary "$binary_path" "$target_path"

if "$target_path" version >/dev/null 2>&1; then
  version_line="$("$target_path" version | head -n 1)"
else
  version_line="$CLI_NAME installed"
fi

log "$version_line"

case ":$PATH:" in
  *":$install_dir:"*)
    ;;
  *)
    log "note: $install_dir is not on PATH"
    ;;
esac
`;
}
