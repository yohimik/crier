#!/bin/sh
# A diagnostic runner: export evidence even when a gate fails. verdict.txt,
# not Docker's successful export, is the acceptance result.
set -eu
out=${1:?usage: accept-tiny.sh output-directory}
: "${TARGETARCH:?}" "${SOURCE_COMMIT:?}" "${TOOLCHAIN_ID:?}"
mkdir -p "$out"
cp /opt/crier-renderer/renderer.sha256 "$out/renderer.sha256"
cp /opt/crier-renderer/crier.mod "$out/build.mod"
find . -type f ! -path './.git/*' -print0 | sort -z | xargs -0 sha256sum > "$out/source-files.sha256"
{
	printf 'source=%s\ntoolchain=%s\ntarget=linux/%s\n' "$SOURCE_COMMIT" "$TOOLCHAIN_ID" "$TARGETARCH"
	tinygo version
	go version
	sha256sum /usr/local/tinygo/bin/tinygo
	printf 'GOFLAGS=%s\n' "$GOFLAGS"
} > "$out/provenance.txt"
if ! timeout 15m go run -tags=rounding ./test/rounding > "$out/rounding-go.log" 2>&1 ||
	! timeout 15m tinygo build -opt=z -no-debug -tags=rounding,noasm -p=2 -o "$out/rounding-tiny" ./test/rounding > "$out/rounding-tiny-build.log" 2>&1 ||
	! timeout 30s "$out/rounding-tiny" > "$out/rounding-tiny.log" 2>&1; then
	echo 'FAIL: explicit rounding regression' > "$out/verdict.txt"
	exit 0
fi
flags="-X github.com/yohimik/crier/internal/version.Version=1.1.1 -X github.com/yohimik/crier/internal/version.Commit=$SOURCE_COMMIT -X github.com/yohimik/crier/internal/version.Date=${BUILD_DATE:-unknown}"
bin="$out/crier-tiny-linux-$TARGETARCH"
if ! timeout 30m tinygo build -opt=z -no-debug -tags=noasm -interp-timeout=15m -p=2 \
	-ldflags "$flags" -o "$bin" ./cmd/crier > "$out/build.log" 2>&1; then
	echo 'FAIL: TinyGo build' > "$out/verdict.txt"
	cat "$out/build.log"
	exit 0
fi
cp "$bin" "$bin.raw"
strip --strip-all "$bin"
if ! CGO_ENABLED=0 timeout 15m go build -trimpath -ldflags "-s -w $flags" \
	-o "$out/crier-go-linux-$TARGETARCH" ./cmd/crier > "$out/build-go.log" 2>&1; then
	echo 'FAIL: Go reference build' > "$out/verdict.txt"
	cat "$out/build-go.log"
	exit 0
fi
wc -c "$bin.raw" "$bin" "$out/crier-go-linux-$TARGETARCH" > "$out/sizes.txt"
sha256sum "$bin.raw" "$bin" "$out/crier-go-linux-$TARGETARCH" > "$out/sha256.txt"
if ! timeout 30s "$bin" --version >> "$out/provenance.txt" 2>&1; then
	echo 'FAIL: TinyGo startup' > "$out/verdict.txt"
	exit 0
fi
status=0
for flavor in raw stripped; do
	case "$flavor" in raw) tested="$bin.raw" ;; *) tested="$bin" ;; esac
	if CRIER_E2E_BINARY="$tested" CRIER_E2E_COMPILER=tinygo \
		timeout 65m go test -tags=e2e ./test/e2e -json -count=1 -timeout=60m > "$out/e2e-$flavor.json" 2>&1; then
		jq -s -e 'any(.[]; .Action == "pass" and .Package == "github.com/yohimik/crier/test/e2e" and (has("Test") | not)) and all(.[]; .Action != "skip" and .Action != "fail")' "$out/e2e-$flavor.json" || status=1
	else
		status=1
	fi
	jq -s '{passed:([.[] | select(.Action == "pass" and has("Test"))] | length),failed:([.[] | select(.Action == "fail")] | length),skipped:([.[] | select(.Action == "skip")] | length)}' \
		"$out/e2e-$flavor.json" > "$out/counts-$flavor.json" || status=1
	cat "$out/counts-$flavor.json"
done
if ! CRIER_PIXEL_REFERENCE="$out/crier-go-linux-$TARGETARCH" CRIER_PIXEL_BINARY="$bin" CRIER_PIXEL_OUTPUT="$out/pixels" \
	timeout 15m go test -tags=pixels ./test/pixels -json -count=1 -timeout=12m > "$out/pixels.json" 2>&1; then
	status=1
fi
if [ "$status" -eq 0 ]; then echo PASS > "$out/verdict.txt"; else echo FAIL > "$out/verdict.txt"; fi
cat "$out/verdict.txt"
