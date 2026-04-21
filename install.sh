#!/usr/bin/env bash
# vctl installer
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/smallest-inc/velocity-cli/main/install.sh | bash
#
# Default install dir:
#   - /usr/local/bin   if it already exists and is writable (or running as root)
#   - $HOME/.local/bin otherwise (created if missing; no sudo required)
#
# Env overrides:
#   VCTL_VERSION       install a specific version (default: latest release)
#   VCTL_INSTALL_DIR   target directory         (default: see above)

set -euo pipefail

REPO="smallest-inc/velocity-cli"
BIN="vctl"
VERSION="${VCTL_VERSION:-}"

choose_install_dir() {
  if [ -n "${VCTL_INSTALL_DIR:-}" ]; then
    printf '%s' "$VCTL_INSTALL_DIR"; return
  fi
  if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
    printf '%s' /usr/local/bin; return
  fi
  if [ "$(id -u)" -eq 0 ]; then
    printf '%s' /usr/local/bin; return
  fi
  if [ -n "${HOME:-}" ]; then
    printf '%s' "$HOME/.local/bin"; return
  fi
  printf '%s' /usr/local/bin
}
INSTALL_DIR=$(choose_install_dir)

log()  { printf '%s\n' "$*" >&2; }
err()  { printf 'error: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || err "required command not found: $1"; }

need curl
need tar
need uname

# --- Detect OS / arch --------------------------------------------------------

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
  darwin|linux) ;;
  *) err "unsupported OS: $OS" ;;
esac

ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) err "unsupported architecture: $ARCH" ;;
esac

# --- Resolve version ---------------------------------------------------------

if [ -z "$VERSION" ]; then
  VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' | head -1 | cut -d '"' -f 4) \
    || err "failed to query latest release"
fi
[ -n "$VERSION" ] || err "could not determine version"

log "Installing ${BIN} ${VERSION} for ${OS}/${ARCH}"

# --- Download + verify -------------------------------------------------------

TARBALL="${BIN}_${VERSION#v}_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

log "Downloading ${TARBALL}"
curl -fsSL -o "$TMPDIR/$TARBALL" "${BASE_URL}/${TARBALL}" \
  || err "failed to download ${BASE_URL}/${TARBALL}"

verify_checksum() {
  local tool
  if command -v sha256sum >/dev/null 2>&1; then
    tool="sha256sum"
  elif command -v shasum >/dev/null 2>&1; then
    tool="shasum -a 256"
  else
    log "warn: no sha256 tool found; skipping checksum verification"
    return
  fi

  if ! curl -fsSL -o "$TMPDIR/checksums.txt" "${BASE_URL}/checksums.txt"; then
    log "warn: checksums.txt unavailable; skipping checksum verification"
    return
  fi

  local expected actual
  expected=$(awk -v f="$TARBALL" '$2==f {print $1}' "$TMPDIR/checksums.txt")
  [ -n "$expected" ] || err "checksum for ${TARBALL} not listed in checksums.txt"
  actual=$(cd "$TMPDIR" && $tool "$TARBALL" | awk '{print $1}')
  [ "$expected" = "$actual" ] || err "checksum mismatch for ${TARBALL}"
  log "Checksum verified"
}
verify_checksum

tar xzf "$TMPDIR/$TARBALL" -C "$TMPDIR" || err "failed to extract ${TARBALL}"
[ -f "$TMPDIR/$BIN" ] || err "archive did not contain expected binary: ${BIN}"
chmod +x "$TMPDIR/$BIN"

# --- Install -----------------------------------------------------------------

# Run a command as root when needed. `sudo </dev/tty` lets the password prompt
# reach the terminal even when this script is fed over `curl | bash`.
run_root() {
  if [ "$(id -u)" -eq 0 ]; then
    "$@"
  elif command -v sudo >/dev/null 2>&1; then
    if [ -e /dev/tty ]; then
      sudo "$@" </dev/tty
    else
      sudo -n "$@" 2>/dev/null \
        || err "cannot elevate non-interactively; re-run as root or set VCTL_INSTALL_DIR to a writable path (e.g. \$HOME/.local/bin)"
    fi
  else
    err "need root to write to ${INSTALL_DIR} but sudo is unavailable"
  fi
}

# Try without elevation first — works for $HOME/.local/bin and pre-writable
# system dirs. Fall back to sudo only if we truly can't write.
if mkdir -p "$INSTALL_DIR" 2>/dev/null && [ -w "$INSTALL_DIR" ]; then
  mv "$TMPDIR/$BIN" "$INSTALL_DIR/$BIN"
else
  log "Installing to ${INSTALL_DIR} (requires elevated privileges)"
  run_root mkdir -p "$INSTALL_DIR"
  run_root mv "$TMPDIR/$BIN" "$INSTALL_DIR/$BIN"
fi

# --- PATH hint + shadowing warning ------------------------------------------

case ":${PATH}:" in
  *":${INSTALL_DIR}:"*) ;;
  *)
    log ""
    log "note: ${INSTALL_DIR} is not in your PATH. Add it to your shell profile:"
    log "        export PATH=\"${INSTALL_DIR}:\$PATH\""
    ;;
esac

if command -v "$BIN" >/dev/null 2>&1; then
  ON_PATH=$(command -v "$BIN")
  if [ "$ON_PATH" != "${INSTALL_DIR}/${BIN}" ]; then
    log ""
    log "note: another ${BIN} is earlier on PATH: ${ON_PATH}"
    log "      remove it or reorder PATH so ${INSTALL_DIR} comes first"
  fi
fi

log ""
log "${BIN} ${VERSION} installed to ${INSTALL_DIR}/${BIN}"
log ""
log "Get started:"
log "  ${BIN} auth login --token <your-pat>"
