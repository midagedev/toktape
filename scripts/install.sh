#!/bin/sh
# toktape installer.
#
#   curl -fsSL https://raw.githubusercontent.com/midagedev/toktape/main/scripts/install.sh | sh
#
# Downloads the release archive for this machine, verifies it against the
# release's checksums.txt, and drops a single static binary into
# $TOKTAPE_INSTALL (default ~/.local/bin).
#
# Environment:
#   TOKTAPE_VERSION   pin a release, e.g. v0.1.0 (default: the latest release)
#   TOKTAPE_INSTALL   install directory (default: ~/.local/bin)
#   TOKTAPE_BASE_URL  where the assets live (default: the GitHub release for
#                     that tag). Point it at a mirror, or at a file:// directory
#                     to install on a box with no route to github.com.
#
# Flags:
#   --dry-run   print the archive URL and the target path, install nothing
#   --help      this text
#
# POSIX sh on purpose: it has to run under dash, busybox ash and macOS' sh.
set -eu

REPO="midagedev/toktape"
BIN="toktape"

DRY_RUN=0
TMPDIR_TOKTAPE=""

die() {
	printf 'install.sh: %s\n' "$*" >&2
	exit 1
}

note() {
	printf '%s\n' "$*" >&2
}

have() {
	command -v "$1" >/dev/null 2>&1
}

cleanup() {
	[ -n "$TMPDIR_TOKTAPE" ] && rm -rf "$TMPDIR_TOKTAPE"
	return 0
}

# usage is inlined rather than read back out of $0: under `curl | sh` there is
# no script file to read.
usage() {
	cat <<EOF
toktape installer.

  curl -fsSL https://raw.githubusercontent.com/$REPO/main/scripts/install.sh | sh

Environment:
  TOKTAPE_VERSION   pin a release, e.g. v0.1.0 (default: the latest release)
  TOKTAPE_INSTALL   install directory (default: ~/.local/bin)
  TOKTAPE_BASE_URL  asset mirror, or a file:// directory for an airgapped box

Flags:
  --dry-run   print the archive URL and the target path, install nothing
  --help      this text
EOF
}

# detect_os maps uname -s onto the GOOS values goreleaser builds.
#
# Windows is built and released, but as a .zip, and this script unpacks tar.gz
# with the tools a POSIX box already has. Rather than grow an unzip dependency
# into a curl-pipe-sh installer, the MSYS/Cygwin cases name the asset and stop:
# a Git Bash user reading "toktape ships no Windows binary" would be reading
# something untrue.
detect_os() {
	os=$(uname -s | tr '[:upper:]' '[:lower:]')
	case "$os" in
	linux) printf 'linux' ;;
	darwin) printf 'darwin' ;;
	mingw* | msys* | cygwin*)
		die "this installer unpacks tar.gz and Windows ships a zip. Download toktape_<version>_windows_$(detect_arch).zip from https://github.com/$REPO/releases/latest and put toktape.exe on your PATH — or run the installer inside WSL2, where the linux binary reads /proc" ;;
	*) die "unsupported OS '$os'; toktape ships linux, darwin and windows binaries. Build from source: go install github.com/$REPO/cmd/$BIN@latest" ;;
	esac
}

# detect_arch maps uname -m onto the GOARCH values goreleaser builds.
detect_arch() {
	arch=$(uname -m)
	case "$arch" in
	x86_64 | amd64) printf 'amd64' ;;
	aarch64 | arm64) printf 'arm64' ;;
	*) die "unsupported architecture '$arch'; toktape ships amd64 and arm64 binaries. Build from source: go install github.com/$REPO/cmd/$BIN@latest" ;;
	esac
}

# fetch writes the body of a URL to stdout.
fetch() {
	if have curl; then
		curl -fsSL "$1"
	elif have wget; then
		wget -qO- "$1"
	else
		die "need curl or wget on PATH"
	fi
}

# download writes a URL to a file.
download() {
	if have curl; then
		curl -fsSL -o "$2" "$1"
	elif have wget; then
		wget -qO "$2" "$1"
	else
		die "need curl or wget on PATH"
	fi
}

# latest_tag asks the GitHub API for the newest release tag. No jq: the field
# is read with grep and sed so the script stays dependency-free.
latest_tag() {
	body=$(fetch "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null) || body=""
	tag=$(printf '%s\n' "$body" |
		grep -m1 '"tag_name"' |
		sed -e 's/.*"tag_name"[[:space:]]*:[[:space:]]*"//' -e 's/".*//') || tag=""
	if [ -z "$tag" ]; then
		die "no published release found for $REPO. Pin one with TOKTAPE_VERSION=v0.1.0, or install from source: go install github.com/$REPO/cmd/$BIN@latest"
	fi
	printf '%s' "$tag"
}

# sha256_of prints the hex digest of a file, using whichever tool the box has.
sha256_of() {
	if have sha256sum; then
		sha256sum "$1" | cut -d' ' -f1
	elif have shasum; then
		shasum -a 256 "$1" | cut -d' ' -f1
	else
		die "need sha256sum or shasum to verify the download"
	fi
}

# verify compares the archive against its line in checksums.txt. A release we
# cannot verify is not installed.
verify() {
	archive=$1
	sums=$2
	name=$3
	want=$(grep -F "  $name" "$sums" | head -n1 | cut -d' ' -f1) || want=""
	[ -n "$want" ] || die "$name is not listed in checksums.txt"
	got=$(sha256_of "$archive")
	[ "$want" = "$got" ] || die "checksum mismatch for $name: expected $want, got $got"
	note "checksum ok  $name"
}

main() {
	for arg in "$@"; do
		case "$arg" in
		--dry-run) DRY_RUN=1 ;;
		-h | --help)
			usage
			return 0
			;;
		*) die "unknown argument '$arg' (try --help)" ;;
		esac
	done

	os=$(detect_os)
	arch=$(detect_arch)

	tag=${TOKTAPE_VERSION:-}
	[ -n "$tag" ] || tag=$(latest_tag)
	# The tag carries the v, the archive name does not: goreleaser's .Version
	# is the tag with the leading v stripped.
	ver=${tag#v}

	# This name is the other half of archives.name_template in .goreleaser.yaml;
	# change one and you must change the other.
	name="${BIN}_${ver}_${os}_${arch}.tar.gz"
	base=${TOKTAPE_BASE_URL:-"https://github.com/$REPO/releases/download/$tag"}
	url="$base/$name"

	dir=${TOKTAPE_INSTALL:-"$HOME/.local/bin"}
	target="$dir/$BIN"

	if [ "$DRY_RUN" -eq 1 ]; then
		printf 'platform:  %s/%s\n' "$os" "$arch"
		printf 'version:   %s\n' "$tag"
		printf 'archive:   %s\n' "$url"
		printf 'checksums: %s\n' "$base/checksums.txt"
		printf 'target:    %s\n' "$target"
		return 0
	fi

	TMPDIR_TOKTAPE=$(mktemp -d 2>/dev/null || mktemp -d -t toktape)
	trap cleanup EXIT INT TERM

	note "downloading $url"
	download "$url" "$TMPDIR_TOKTAPE/$name" ||
		die "could not download $url — check that $tag has a $os/$arch asset"
	download "$base/checksums.txt" "$TMPDIR_TOKTAPE/checksums.txt" ||
		die "could not download $base/checksums.txt"
	verify "$TMPDIR_TOKTAPE/$name" "$TMPDIR_TOKTAPE/checksums.txt" "$name"

	tar -xzf "$TMPDIR_TOKTAPE/$name" -C "$TMPDIR_TOKTAPE" "$BIN" ||
		die "could not extract $BIN from $name"

	mkdir -p "$dir" || die "could not create $dir"
	# Move onto the target through a temp name in the same directory so a
	# running toktape is replaced atomically instead of being truncated.
	mv "$TMPDIR_TOKTAPE/$BIN" "$target.new" || die "could not write $target.new"
	chmod 0755 "$target.new"
	mv "$target.new" "$target" || die "could not install $target"

	note "installed $target"
	case ":$PATH:" in
	*":$dir:"*) ;;
	*)
		note ""
		note "$dir is not on your PATH. Add it:"
		note "  export PATH=\"\$PATH:$dir\""
		note ""
		;;
	esac

	"$target" version
}

main "$@"
