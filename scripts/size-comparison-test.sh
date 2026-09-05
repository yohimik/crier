#!/bin/sh
set -eu
cd /src
out=/src/coverage/size-comparison
{
  go version
  ffmpeg -version
  uname -a
  id
} > "$out/test-environment-linux-arm64.log"
for compiler in go tinygo tinygo-stripped; do
  bin="$out/bin/$compiler-linux-arm64"
  "$bin" --version > "$out/version-$compiler-linux-arm64.log" 2>&1
  code=0
  CRIER_E2E_BINARY="$bin" timeout 1800 go test -tags e2e ./test/e2e \
    -count=1 -timeout 25m -v > "$out/e2e-$compiler-linux-arm64.log" 2>&1 || code=$?
  printf '%s\n' "$code" > "$out/e2e-$compiler-linux-arm64.exit"
  printf '%s exit=%s\n' "$compiler" "$code"
done
