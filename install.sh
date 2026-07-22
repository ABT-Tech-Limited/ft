#!/bin/sh
# ft installer: download the latest release for this machine, verify its
# SHA-256 against checksums.txt, and install it to /usr/local/bin/ft.
#
#   curl --proto '=https' -fsSL https://raw.githubusercontent.com/ABT-Tech-Limited/ft/main/install.sh | sh
set -eu

REPO="ABT-Tech-Limited/ft"
INSTALL_DIR="/usr/local/bin"

fetch() {
	curl --proto '=https' --proto-redir '=https' -fsSL "$1" -o "$2"
}

main() {
	if [ "$(uname -s)" != "Linux" ]; then
		echo "ft supports Linux only; detected $(uname -s)" >&2
		exit 1
	fi
	case "$(uname -m)" in
		x86_64) arch="amd64" ;;
		aarch64 | arm64) arch="arm64" ;;
		*) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
	esac

	tmp_dir="$(mktemp -d)"
	trap 'rm -rf "$tmp_dir"' EXIT

	echo "Resolving the latest ft release..."
	fetch "https://api.github.com/repos/$REPO/releases/latest" "$tmp_dir/release.json"
	version="$(sed -n 's/.*"tag_name":[[:space:]]*"v\([^"]*\)".*/\1/p' "$tmp_dir/release.json")"
	case "$version" in
		"" | *[!0-9A-Za-z.+-]*) echo "could not resolve a valid release version" >&2; exit 1 ;;
	esac

	archive="ft_${version}_linux_${arch}.tar.gz"
	echo "Downloading ft v$version (linux/$arch)..."
	fetch "https://github.com/$REPO/releases/latest/download/$archive" "$tmp_dir/$archive"
	fetch "https://github.com/$REPO/releases/latest/download/checksums.txt" "$tmp_dir/checksums.txt"

	if ! expected="$(
		awk -v name="$archive" '
			$2 == name { count++; hash = $1 }
			END { if (count != 1) exit 1; print hash }
		' "$tmp_dir/checksums.txt"
	)"; then
		echo "checksums.txt does not list exactly one entry for $archive" >&2
		exit 1
	fi
	actual="$(sha256sum "$tmp_dir/$archive")"
	actual="${actual%% *}"
	if [ "$actual" != "$expected" ]; then
		echo "SHA-256 mismatch for $archive" >&2
		exit 1
	fi

	tar -xzf "$tmp_dir/$archive" -C "$tmp_dir" ft
	if [ "$(id -u)" -eq 0 ]; then
		install -m 0755 -- "$tmp_dir/ft" "$INSTALL_DIR/ft"
	else
		sudo install -m 0755 -- "$tmp_dir/ft" "$INSTALL_DIR/ft"
	fi
	echo "Installed $INSTALL_DIR/ft:"
	"$INSTALL_DIR/ft" version
}

main "$@"
