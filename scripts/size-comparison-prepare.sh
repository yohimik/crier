#!/bin/sh
set -eu
root=$(CDPATH='' cd -- "$(dirname "$0")/.." && pwd)
cache=${SIZE_COMPARISON_CACHE:-/Users/yohimik/.cache/tinygo-spike-darwin}
net=${SIZE_COMPARISON_NET:-/Users/yohimik/Projects/tinygo/src/net}
out="$root/coverage/size-comparison"
[ "$(git -C "$net" rev-parse HEAD)" = 476a4e3241ee8061a8ae6a311884f6304fda7ea0 ] || { echo 'Use the net commit specified in the report.' >&2; exit 1; }
git -C "$net" diff --quiet HEAD
[ ! -e "$out/toolchain" ] || { echo 'The toolchain snapshot already exists.' >&2; exit 1; }
mkdir -p "$out/toolchain"
rsync -a "$cache/tinygo/" "$out/toolchain/"
rsync -a --exclude .git "$net/" "$out/toolchain/src/net/"
mkdir -p "$out/toolchain/src/net/http/cookiejar"
cp "$cache/go/src/net/http/cookiejar/jar.go" \
  "$cache/go/src/net/http/cookiejar/punycode.go" "$out/toolchain/src/net/http/cookiejar/"
git -C "$root" rev-parse HEAD > "$out/source-commit.txt"
git -C "$net" rev-parse HEAD > "$out/net-commit.txt"
shasum -a 256 "$cache/go/bin/go" "$out/toolchain/bin/tinygo" > "$out/compilers.sha256"
