#!/bin/sh
# Builds a minimal static libpcap into the prefix given as $1, for linking
# into tcpview's Linux release binaries.
#
# Why: linking the runner's own libpcap ties the binary to that distro's
# soname (Ubuntu ships libpcap.so.0.8, Arch only libpcap.so.1), so the
# release failed to start on half of Linux. Linking libpcap statically
# leaves glibc as the only shared dependency. Every optional capture
# backend (USB, Bluetooth, D-Bus, RDMA, netmap, DPDK, remote pcap) is
# compiled out: they'd drag in shared libraries of their own, and none of
# them carries network traffic.
#
# The tarball is pinned by sha256, and CVE-2026-0799.patch (a backport of
# upstream commit 48e8960a7108, taken from Ubuntu's 1.10.4-4.1ubuntu3.1)
# fixes the BPF interpreter's unchecked scratch-memory index. tcpview
# never installs a userspace BPF filter, but a static binary shouldn't
# ship known-vulnerable code either way.
#
# Set LIBPCAP_TARBALL to a local copy of the tarball to skip the download.
set -eu

prefix=$1
version=1.10.4
sha256=ed19a0383fad72e3ad435fd239d7cd80d64916b87269550159d20e47160ebe5f
here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

tarball=${LIBPCAP_TARBALL:-}
if [ -z "$tarball" ]; then
  tarball="$work/libpcap-$version.tar.gz"
  curl -sSfL -o "$tarball" "https://www.tcpdump.org/release/libpcap-$version.tar.gz"
fi
echo "$sha256  $tarball" | sha256sum -c -

tar xzf "$tarball" -C "$work"
cd "$work/libpcap-$version"
patch -p1 < "$here/CVE-2026-0799.patch"
./configure --prefix="$prefix" --disable-shared --enable-static \
  --disable-usb --disable-netmap --disable-bluetooth --disable-dbus \
  --disable-rdma --disable-remote --without-libnl --without-dpdk
make -j"$(nproc)"
make install
