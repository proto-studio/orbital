#!/usr/bin/env bash
# Install a pinned sccache binary into /usr/local/bin for CI compile caching.
# Supports the runners this project builds on: linux/amd64 and darwin/arm64.
set -euo pipefail

VERSION="${SCCACHE_VERSION:-v0.8.2}"

uname_s="$(uname -s)"
uname_m="$(uname -m)"
case "${uname_s}/${uname_m}" in
	Linux/x86_64)   triple="x86_64-unknown-linux-musl" ;;
	Darwin/arm64)   triple="aarch64-apple-darwin" ;;
	Darwin/x86_64)  triple="x86_64-apple-darwin" ;;
	Linux/aarch64)  triple="aarch64-unknown-linux-musl" ;;
	*) echo "ERROR: unsupported host for sccache: ${uname_s}/${uname_m}" >&2; exit 1 ;;
esac

if command -v sccache >/dev/null 2>&1; then
	echo ">>> sccache already installed: $(sccache --version)"
	exit 0
fi

pkg="sccache-${VERSION}-${triple}"
url="https://github.com/mozilla/sccache/releases/download/${VERSION}/${pkg}.tar.gz"

tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

echo ">>> Downloading ${url}"
curl -fsSL "${url}" -o "${tmp}/sccache.tar.gz"
tar -xzf "${tmp}/sccache.tar.gz" -C "${tmp}"

dest="/usr/local/bin/sccache"
if [ -w "$(dirname "${dest}")" ]; then
	mv "${tmp}/${pkg}/sccache" "${dest}"
else
	sudo mv "${tmp}/${pkg}/sccache" "${dest}"
	sudo chmod +x "${dest}"
fi
chmod +x "${dest}" 2>/dev/null || true

echo ">>> Installed: $(sccache --version)"
