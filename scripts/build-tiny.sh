#!/bin/sh
# Build and strip both additive Linux assets. The caller supplies the same
# version, commit and date used for the six standard Go assets.
set -eu
out=${1:?usage: build-tiny.sh output-directory}
: "${DISPAT_VERSION:?}" "${DISPAT_COMMIT:?}" "${DISPAT_DATE:?}"
mkdir -p "$out"
{
	tinygo version
	go version
	cat /opt/crier-renderer/renderer.sha256
	printf 'flags=-opt=z -no-debug -tags=noasm -interp-timeout=15m -p=2\n'
} > "$out/tinygo-build-info.txt"
for arch in amd64 arm64; do
	name=crier-tiny-linux-$arch
	case "$arch" in
	amd64) strip_bin=x86_64-linux-gnu-strip ;;
	arm64) strip_bin=aarch64-linux-gnu-strip ;;
	esac
	GOOS=linux GOARCH=$arch timeout 30m tinygo build -opt=z -no-debug \
		-tags=noasm -interp-timeout=15m -p=2 \
		-ldflags "-X github.com/yohimik/crier/internal/version.Version=$DISPAT_VERSION -X github.com/yohimik/crier/internal/version.Commit=$DISPAT_COMMIT -X github.com/yohimik/crier/internal/version.Date=$DISPAT_DATE" \
		-o "$out/$name" ./cmd/crier > "$out/$name.build.log" 2>&1 || {
		cat "$out/$name.build.log" >&2
		exit 1
	}
	raw=$(wc -c < "$out/$name" | tr -d ' ')
	"$strip_bin" --strip-all "$out/$name"
	case "$arch" in
	amd64) machine='Advanced Micro Devices X86-64' ;;
	arm64) machine=AArch64 ;;
	esac
	readelf -h "$out/$name" | grep -q "$machine" || {
		echo "$name has the wrong ELF machine" >&2; exit 1;
	}
	if readelf -l "$out/$name" | grep -q INTERP || readelf -d "$out/$name" | grep -q NEEDED; then
		echo "$name is not a static binary" >&2; exit 1
	fi
	size=$(wc -c < "$out/$name" | tr -d ' ')
	printf '%s raw=%s stripped=%s sha256=%s\n' "$name" "$raw" "$size" \
		"$(sha256sum "$out/$name" | cut -d' ' -f1)" >> "$out/tinygo-build-info.txt"
done
cat "$out/tinygo-build-info.txt"
