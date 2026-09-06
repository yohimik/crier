#!/bin/sh
set -eu
root=$(CDPATH='' cd -- "$(dirname "$0")/.." && pwd)
out="$root/coverage/size-comparison"
cache=${SIZE_COMPARISON_CACHE:-/Users/yohimik/.cache/tinygo-spike-darwin}
export PATH="$cache/go/bin:$out/toolchain/bin:$PATH"
export GOPATH="$cache/gopath" TINYGOROOT="$out/toolchain"
export GOMAXPROCS=2 GOMEMLIMIT=8GiB GOTOOLCHAIN=local CGO_ENABLED=0
export GOOS=linux GOARCH=arm64
unset GOFLAGS GOWORK
cd "$root"
/usr/bin/time -l go build -p 2 -trimpath -ldflags '-s -w' \
  -o "$out/bin/renderer-go-linux-arm64" ./tools/size-comparison/renderer \
  > "$out/build-renderer-go-linux-arm64.log" 2>&1
/usr/bin/time -l tinygo build -p 2 -opt=z -no-debug -tags noasm -interp-timeout=15m \
  -o "$out/bin/renderer-tinygo-linux-arm64" ./tools/size-comparison/renderer \
  > "$out/build-renderer-tinygo-linux-arm64.log" 2>&1
mkdir -p "$out/sufake"
sed -n '/^COPY --chown=gopher:gopher <<.SUFAKE. /,/^SUFAKE$/p' Dockerfile.tinygo \
  | sed '1d;$d' > "$out/sufake/main.go"
go build -p 2 -o "$out/bin/sufake-linux-arm64" "$out/sufake/main.go"
go build -p 2 -trimpath -ldflags '-s -w -X github.com/yohimik/crier/internal/version.Version=1.1.0' \
  -o "$out/bin/update-linux-arm64" ./cmd/crier
