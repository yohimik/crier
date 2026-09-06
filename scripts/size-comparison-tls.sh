#!/bin/sh
set -eu
cd /src
out=/src/coverage/size-comparison
work=$(mktemp -d "$out/tls.XXXXXX")
server=
trap 'if [ -n "$server" ]; then kill "$server" 2>/dev/null || true; fi' EXIT
for compiler in go tinygo tinygo-stripped; do
  ca="$work/$compiler.pem"
  serverlog="$out/tls-server-$compiler.log"
  "$out/bin/sufake-linux-arm64" -addr 127.0.0.1:0 -ca "$ca" \
    -asset "$out/bin/update-linux-arm64" -asset-name crier-linux-arm64 \
    -version 1.1.0 > "$serverlog" 2>&1 &
  server=$!
  i=0
  until grep -q 'sufake: ca' "$serverlog"; do
    i=$((i+1))
    [ "$i" -lt 100 ] || exit 1
    sleep 0.1
  done
  base=$(sed -n 's/^sufake: listening //p' "$serverlog")
  cp "$out/bin/$compiler-linux-arm64" "$work/crier-$compiler"
  bin="$work/crier-$compiler"
  log="$out/tls-$compiler-linux-arm64.log"
  : > "$log"
  check() {
    label=$1 expected=$2
    shift 2
    code=0
    timeout 90 "$@" > "$work/row.log" 2>&1 || code=$?
    printf '%s exit=%s expected=%s\n' "$label" "$code" "$expected" >> "$log"
    cat "$work/row.log" >> "$log"
    [ "$code" -eq "$expected" ]
  }
  check trusted-name 1 env SSL_CERT_FILE="$ca" "$bin" self-update --api-url "$base" --check
  grep -q '1.1.0' "$work/row.log"
  check trusted-ip 1 env SSL_CERT_FILE="$ca" "$bin" self-update --api-url "https://127.0.0.1:${base##*:}" --check
  grep -q '1.1.0' "$work/row.log"
  code=0
  timeout 90 env SSL_CERT_FILE= SSL_CERT_DIR="$work/no-roots" "$bin" self-update --api-url "$base" --check > "$work/row.log" 2>&1 || code=$?
  printf 'untrusted exit=%s\n' "$code" >> "$log"
  cat "$work/row.log" >> "$log"
  [ "$code" -ne 0 ] && [ "$code" -ne 124 ]
  grep -Eq 'certificate|x509' "$work/row.log"
  code=0
  timeout 90 "$bin" self-update --api-url "http://localhost:${base##*:}" --check > "$work/row.log" 2>&1 || code=$?
  printf 'plaintext exit=%s\n' "$code" >> "$log"
  cat "$work/row.log" >> "$log"
  [ "$code" -ne 0 ] && [ "$code" -ne 124 ]
  check full-update 0 env SSL_CERT_FILE="$ca" "$bin" self-update --api-url "$base"
  "$bin" --version > "$work/version.log"
  grep -q '^crier 1.1.0 ' "$work/version.log"
  cat "$work/version.log" >> "$log"
  "$bin.backup" --version >> "$log"
  kill "$server"
  wait "$server" 2>/dev/null || true
  server=
  check rollback 0 "$bin" self-update --rollback
  "$bin" --version >> "$log"
  grep -q 'handshake=true' "$serverlog"
  grep -q 'clienthello=true' "$serverlog"
  grep -q 'clienthello=false' "$serverlog"
done
