#!/bin/sh
# Installs tcpview from a GitHub release.
#
#   curl -sSL https://raw.githubusercontent.com/jtrull101/tcpview/main/install.sh | sh
#
# Env vars:
#   VERSION      release tag to install, e.g. "v0.1.0" (default: latest)
#   INSTALL_DIR  where the binary goes (default: /usr/local/bin)
#
# /usr/local/bin by default so that "sudo tcpview" works: sudo ignores your
# PATH in favour of its own secure_path, which includes /usr/local/bin on
# every mainstream distro but never ~/.local/bin. If the directory isn't
# writable, only the final copy into it runs under sudo.
set -eu

REPO="jtrull101/tcpview"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

os() {
	case "$(uname -s)" in
	Linux) echo linux ;;
	Darwin) echo darwin ;;
	MINGW* | MSYS* | CYGWIN*)
		echo "tcpview: on Windows, download tcpview-<version>-windows-amd64.zip from" >&2
		echo "  https://github.com/${REPO}/releases/latest and install Npcap (https://npcap.com/#download)" >&2
		exit 1
		;;
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

# Use sudo for the copy only when the target isn't writable as-is (it
# usually isn't, for /usr/local/bin), and say so before it prompts.
AS_ROOT=""
if ! mkdir -p "$INSTALL_DIR" 2>/dev/null || [ ! -w "$INSTALL_DIR" ]; then
	if [ "$(id -u)" -ne 0 ] && command -v sudo >/dev/null 2>&1; then
		echo "tcpview: ${INSTALL_DIR} needs root to write to - using sudo for the copy"
		AS_ROOT="sudo"
	else
		echo "tcpview: can't write to ${INSTALL_DIR} - re-run with sudo, or pick a directory you own, e.g.:" >&2
		echo "  curl -sSL https://raw.githubusercontent.com/${REPO}/main/install.sh | INSTALL_DIR=\$HOME/.local/bin sh" >&2
		exit 1
	fi
fi
$AS_ROOT mkdir -p "$INSTALL_DIR"
$AS_ROOT install -m 0755 "${TMPDIR}/tcpview-${VERSION}-${OS}-${ARCH}/tcpview" "${INSTALL_DIR}/tcpview"

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
fi

# sudo only finds commands on its secure_path; warn when a custom
# INSTALL_DIR isn't on it, rather than leave "sudo tcpview" failing with
# "command not found".
case ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:" in
*":${INSTALL_DIR}:"*) RUN="sudo tcpview" ;;
*)
	RUN="sudo ${INSTALL_DIR}/tcpview"
	echo "tcpview: note - sudo won't find tcpview in ${INSTALL_DIR} by name; use the full path" >&2
	;;
esac
echo "tcpview: run it with: ${RUN}"
if [ "$OS" = "linux" ]; then
	echo "  (or skip sudo from now on: sudo setcap cap_net_raw,cap_net_admin=eip ${INSTALL_DIR}/tcpview)"
fi
