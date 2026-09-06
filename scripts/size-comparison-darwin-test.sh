#!/bin/sh
set -eu
root=$(CDPATH='' cd -- "$(dirname "$0")/.." && pwd)
cache=${SIZE_COMPARISON_CACHE:-/Users/yohimik/.cache/tinygo-spike-darwin}
export PATH="$cache/go/bin:/opt/homebrew/bin:$PATH"
export GOPATH="$cache/gopath" GOMAXPROCS=2 GOMEMLIMIT=8GiB GOTOOLCHAIN=local
export GOFLAGS=-p=2
unset GOOS GOARCH GOWORK
cd "$root"
out="$root/coverage/size-comparison"
for compiler in go tinygo; do
  code=0
  CRIER_E2E_BINARY="$out/bin/$compiler-darwin-arm64" go test -tags e2e ./test/e2e \
    -count=1 -timeout 10m -v > "$out/e2e-$compiler-darwin-arm64.log" 2>&1 || code=$?
  printf '%s\n' "$code" > "$out/e2e-$compiler-darwin-arm64.exit"
  printf '%s exit=%s\n' "$compiler" "$code"
done
