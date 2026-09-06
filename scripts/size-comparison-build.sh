#!/bin/sh
set -eu
root=$(CDPATH='' cd -- "$(dirname "$0")/.." && pwd)
out="$root/coverage/size-comparison"
cache=${SIZE_COMPARISON_CACHE:-/Users/yohimik/.cache/tinygo-spike-darwin}
export PATH="$cache/go/bin:$out/toolchain/bin:$PATH"
export GOPATH="$cache/gopath" TINYGOROOT="$out/toolchain"
export GOMAXPROCS=2 GOMEMLIMIT=8GiB GOTOOLCHAIN=local CGO_ENABLED=0
unset GOFLAGS GOWORK
cd "$root"
git diff --quiet 7edaff931ac9367734d806b684389b3ba4caf026 -- cmd internal go.mod go.sum announce examples test
mkdir -p "$out/bin"
target=${1:-linux/arm64}
export GOOS="${target%/*}" GOARCH="${target#*/}"
name="$GOOS-$GOARCH"
v=github.com/yohimik/crier/internal/version
stamp="-X $v.Version=0.0.0-spike -X $v.Commit=7edaff931ac9 -X $v.Date=2026-09-05T00:00:00Z"
go version
tinygo version
/usr/bin/time -l go build -p 2 -trimpath -ldflags "-s -w $stamp" \
  -o "$out/bin/go-$name" ./cmd/crier >"$out/build-go-$name.log" 2>&1
/usr/bin/time -l tinygo build -p 2 -opt=z -no-debug -tags noasm \
  -interp-timeout=15m -ldflags "$stamp" \
  -o "$out/bin/tinygo-$name" ./cmd/crier >"$out/build-tinygo-$name.log" 2>&1
wc -c "$out/bin/go-$name" "$out/bin/tinygo-$name"
shasum -a 256 "$out/bin/go-$name" "$out/bin/tinygo-$name"
