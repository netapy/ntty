#!/bin/sh
# Install ntty from GitHub releases.
#
#   curl -fsSL https://raw.githubusercontent.com/netapy/ntty/main/install.sh | sh
#
# Environment:
#   NTTY_INSTALL_DIR  target directory (default: ~/.local/bin)
#   NTTY_VERSION      release tag to install (default: latest)
set -eu

repo=netapy/ntty
install_dir=${NTTY_INSTALL_DIR:-$HOME/.local/bin}
version=${NTTY_VERSION:-latest}

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*)
		echo "ntty: unsupported architecture: $arch" >&2
		exit 1
		;;
esac
case "$os" in
	darwin | linux) ;;
	*)
		echo "ntty: unsupported operating system: $os" >&2
		exit 1
		;;
esac

if ! command -v curl >/dev/null 2>&1; then
	echo "ntty: curl is required" >&2
	exit 1
fi

if [ "$version" = "latest" ]; then
	version=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" |
		sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
fi
if [ -z "$version" ]; then
	echo "ntty: could not determine the latest release" >&2
	exit 1
fi
number=${version#v}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

archive="ntty_${number}_${os}_${arch}.tar.gz"
base="https://github.com/$repo/releases/download/$version"
echo "Downloading $archive ($version)"
curl -fsSL "$base/$archive" -o "$tmp/$archive"
curl -fsSL "$base/ntty_${number}_checksums.txt" -o "$tmp/checksums.txt"

expected=$(grep " $archive\$" "$tmp/checksums.txt" | awk '{print $1}')
if [ -z "$expected" ]; then
	echo "ntty: no checksum published for $archive" >&2
	exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
	actual=$(sha256sum "$tmp/$archive" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
	actual=$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')
else
	echo "ntty: sha256sum or shasum is required to verify the download" >&2
	exit 1
fi
if [ "$expected" != "$actual" ]; then
	echo "ntty: checksum verification failed" >&2
	exit 1
fi

tar -xzf "$tmp/$archive" -C "$tmp"
mkdir -p "$install_dir"
install -m 755 "$tmp/ntty" "$install_dir/ntty"

echo "Installed ntty $version to $install_dir/ntty"
case ":$PATH:" in
	*":$install_dir:"*) ;;
	*) echo "Add it to your PATH: export PATH=\"$install_dir:\$PATH\"" ;;
esac
