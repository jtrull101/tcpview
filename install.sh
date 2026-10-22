#!/bin/sh
# Installs tcpview from a GitHub release.
#
#   curl -sSL https://raw.githubusercontent.com/jtrull101/tcpview/main/install.sh | sh
#
# Env vars:
#   VERSION      release tag to install, e.g. "v0.1.0" (default: latest)
#   INSTALL_DIR  where the binary goes (default: $HOME/.local/bin)
set -eu

REPO="jtrull101/tcpview"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

os() {
	case "$(uname -s)" in
	Linux) echo linux ;;
	Darwin) echo darwin ;;
	*)
		echo "tcpview: unsupported OS $(uname -s) - no prebuilt binary, build from source instead" >&2
		exit 1
		;;
	esac
}

arch() {
	case "$(uname -m)" in
	x86_64 | amd64) echo amd64 ;;
	arm64 | aarch64) echo arm64 ;;
	*)
		echo "tcpview: unsupported architecture $(uname -m) - no prebuilt binary, build from source instead" >&2
		exit 1
		;;
	esac
}

OS="$(os)"
ARCH="$(arch)"

VERSION="${VERSION:-}"
if [ -z "$VERSION" ]; then
	VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')"
fi
if [ -z "$VERSION" ]; then
	echo "tcpview: could not determine the latest release version" >&2
	exit 1
fi

ASSET="tcpview-${VERSION}-${OS}-${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

echo "tcpview: downloading ${URL}"
curl -fsSL "$URL" -o "${TMPDIR}/${ASSET}"

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

# Releases publish SHA256SUMS alongside the binaries; refuse to install
# anything that doesn't match it. Releases older than that file just
# get a warning.
if curl -fsSL "https://github.com/${REPO}/releases/download/${VERSION}/SHA256SUMS" -o "${TMPDIR}/SHA256SUMS" 2>/dev/null; then
	WANT="$(grep " ${ASSET}\$" "${TMPDIR}/SHA256SUMS" | cut -d' ' -f1)"
	GOT="$(sha256 "${TMPDIR}/${ASSET}")"
	if [ -z "$WANT" ] || [ "$WANT" != "$GOT" ]; then
		echo "tcpview: checksum mismatch for ${ASSET} (expected ${WANT:-nothing listed}, got ${GOT}) - not installing" >&2
		exit 1
	fi
	echo "tcpview: checksum verified"
else
	echo "tcpview: warning - ${VERSION} publishes no SHA256SUMS, skipping checksum verification" >&2
fi

tar -xzf "${TMPDIR}/${ASSET}" -C "$TMPDIR"

mkdir -p "$INSTALL_DIR"
install -m 0755 "${TMPDIR}/tcpview-${VERSION}-${OS}-${ARCH}/tcpview" "${INSTALL_DIR}/tcpview"

echo "tcpview: installed ${VERSION} to ${INSTALL_DIR}/tcpview"

case ":$PATH:" in
*":${INSTALL_DIR}:"*) ;;
*) echo "tcpview: note - ${INSTALL_DIR} is not on your PATH" ;;
esac

if [ "$OS" = "linux" ]; then
	if ldd "${INSTALL_DIR}/tcpview" 2>&1 | grep -q 'libpcap\.so\.0\.8 => not found'; then
		LIBPCAP1="$(find /usr/lib /usr/lib64 /lib /lib64 -name 'libpcap.so.1' 2>/dev/null | head -1)"
		echo "" >&2
		echo "tcpview: your system has libpcap.so.1 but this (older) release needs the" >&2
		echo "  legacy libpcap.so.0.8 soname (a quirk of the Ubuntu runner it was built on;" >&2
		echo "  newer releases link libpcap statically and don't have this problem)." >&2
		if [ -n "$LIBPCAP1" ]; then
			echo "  Fix: sudo ln -s ${LIBPCAP1} $(dirname "$LIBPCAP1")/libpcap.so.0.8" >&2
		fi
		echo "  Or build from source instead: go build -o tcpview ." >&2
	fi
	echo "tcpview: live capture needs CAP_NET_RAW - either run with sudo, or once:" >&2
	echo "  sudo setcap cap_net_raw,cap_net_admin=eip ${INSTALL_DIR}/tcpview" >&2
fi
