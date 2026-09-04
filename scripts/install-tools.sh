#!/bin/sh
# The tools a crier build needs beside Go itself, as one `dispat install` line
# each.
#
# This is the install manifest dispat's own repository keeps under the same
# name, written for this one: the versions are pinned here and read from here
# by everything that installs them. The fork stage of Dockerfile.tinygo and
# the darwin half of the spike both call this file, so moving to a newer fork
# is bumping one number below and nothing else. dispat itself is not installed
# by this file: the container copies it out of the yohimik/dispat-debian
# image, whose tag Dockerfile.tinygo pins, and a Mac has it on PATH.
#
#   sh scripts/install-tools.sh                   every tool
#   sh scripts/install-tools.sh tinygo            one of them, by name
#   sh scripts/install-tools.sh --version tinygo  print a pin and install nothing
#
# The environment it reads:
#
#   INSTALL_TOOLS_DISPAT   the dispat to install with; without it, dispat on
#                          PATH.
#   INSTALL_TOOLS_PREFIX   where a toolchain tree is unpacked; without it,
#                          /usr/local. A toolchain is a bin/ plus the lib/ and
#                          src/ beside it, so it lands under a prefix rather
#                          than as one file in a bin folder.
#   GITHUB_TOKEN           read by `dispat install` for the releases listing.
#                          Optional, and what keeps a shared runner IP off the
#                          unauthenticated rate limit.
set -eu

# The pin. A release version of a repository this repository does not build,
# so there is nothing to derive it from and one place to state it.
TINYGO_FORK_VERSION=0.43.0-net.1

# Every tool this manifest knows, in the order a bare run installs them.
TOOLS="tinygo"

log() { printf 'install-tools: %s\n' "$*" >&2; }

die() {
	log "$*"
	exit 1
}

usage() {
	cat <<EOF
usage: install-tools.sh [--version] [tool...]

Installs the pinned tools; with no arguments, all of them.
Tools: $TOOLS
EOF
}

# The pin of one tool, on standard output. It is the same value the install
# below passes to --release, read from the same variable, so a caller that
# needs the number (a docs check, a cache key, a workflow) can never be told
# a different one than the install uses.
pin() {
	case $1 in
	tinygo) echo "$TINYGO_FORK_VERSION" ;;
	*) die "unknown tool $1; the manifest carries: $TOOLS" ;;
	esac
}

want_version=false
case ${1:-} in
-h | --help)
	usage
	exit 0
	;;
--version)
	want_version=true
	shift
	;;
esac

if [ "$want_version" = true ]; then
	[ $# -eq 1 ] || die "--version takes exactly one tool name"
	pin "$1"
	exit 0
fi

# No arguments is every tool, which is what a fresh machine wants and what
# keeps the list in one place rather than in each caller. The split is the
# point of the unquoted expansion here: TOOLS is a list of names, and the
# names are the shell words this loop wants.
# shellcheck disable=SC2086
[ $# -gt 0 ] || set -- $TOOLS

dispat_bin=${INSTALL_TOOLS_DISPAT:-dispat}
command -v "$dispat_bin" >/dev/null 2>&1 ||
	die "no dispat at $dispat_bin; https://dispat.dev/reference/ci/#the-install-script"

prefix=${INSTALL_TOOLS_PREFIX:-/usr/local}

# The fork's net/http is TinyGo's own tree, taken whole rather than merged
# with Go's (loader/goroot.go there lists "net/http/" as not merged), and that
# tree carries no cookiejar. minio-go, which is crier's S3 stage, imports it
# through golang.org/x/net/publicsuffix, so a fork-built crier stops at that
# import before compiling a line of its own. The package is pure Go over
# net/http's exported API and Go's own copy compiles against the fork's
# net/http unchanged, so it is laid over the installed tree here, from the Go
# the toolchain builds with, until the fork carries it. Deleting this
# function is how that bump is taken.
#
# Only the two source files: the package's tests import a stand-in public
# suffix list that has no business in a toolchain tree.
overlay_cookiejar() {
	dest=$prefix/tinygo/src/net/http/cookiejar
	if [ -f "$dest/jar.go" ]; then
		log "net/http/cookiejar is already laid over $prefix/tinygo"
		return 0
	fi
	command -v go >/dev/null 2>&1 ||
		die "laying net/http/cookiejar over the fork needs go on PATH, the one tinygo builds with"
	src=$(go env GOROOT)/src/net/http/cookiejar
	[ -f "$src/jar.go" ] || die "no net/http/cookiejar under $(go env GOROOT)"
	mkdir -p "$dest"
	cp "$src/jar.go" "$src/punycode.go" "$dest/"
	log "laid Go's net/http/cookiejar over $prefix/tinygo"
}

# The TinyGo fork the spike builds crier with. It is a toolchain tree rather
# than a binary, so --pipe 'tar -xz' unpacks the
# release's tarball into the prefix and the compiler lands at
# <prefix>/tinygo/bin/tinygo with its lib/ and src/ beside it.
#
# A --pipe install has no destination file to compare, so dispat cannot tell
# an installed toolchain from a missing one and would fetch 185 MB every run.
# What the tree reports is the check instead: the right version already there
# is nothing to do, and anything else is thrown away and fetched, because a
# half-unpacked tree is worse than no tree.
install_tinygo() {
	tinygo_bin=$prefix/tinygo/bin/tinygo
	if "$tinygo_bin" version 2>/dev/null | grep -qF "$TINYGO_FORK_VERSION"; then
		log "tinygo $TINYGO_FORK_VERSION is already at $prefix/tinygo"
		overlay_cookiejar
		return 0
	fi
	rm -rf "$prefix/tinygo"
	# --pipe runs its command in --bin-dir, so the prefix has to be a folder
	# before the tarball is unpacked into it. A caller pointing at a cache
	# folder that does not exist yet is the ordinary case, not a mistake.
	mkdir -p "$prefix"
	log "installing tinygo $TINYGO_FORK_VERSION into $prefix"
	"$dispat_bin" install yohimik/tinygo \
		--prerelease --release "$TINYGO_FORK_VERSION" \
		--asset 'tinygo{version}.{os}-{arch}.tar.gz' \
		--bin-dir "$prefix" --pipe 'tar -xz'
	"$tinygo_bin" version | grep -qF "$TINYGO_FORK_VERSION" ||
		die "the installed tinygo does not report $TINYGO_FORK_VERSION"
	log "installed tinygo $TINYGO_FORK_VERSION"
	overlay_cookiejar
}

for tool in "$@"; do
	case $tool in
	tinygo) install_tinygo ;;
	*) die "unknown tool $tool; the manifest carries: $TOOLS" ;;
	esac
done
