#!/bin/sh
# Prepare an isolated, pinned renderer for BOTH release compilers. Never patch
# GOMODCACHE or add a local replace to the distributable root go.mod.
# Run from the repository root; then export GOFLAGS=-modfile=<output>/crier.mod.
set -eu
out=${1:?usage: prepare-renderer.sh new-absolute-output-directory}
case "$out" in
/*) ;;
*) echo 'renderer output must be absolute' >&2; exit 1 ;;
esac
case "$out" in
*[!a-zA-Z0-9_./-]*) echo 'renderer output must not contain spaces or shell metacharacters' >&2; exit 1 ;;
esac
module=github.com/benoitkugler/webrender
version=$(GOFLAGS='' GOWORK=off go list -m -f '{{.Version}}' "$module")
[ "$version" = v0.0.14 ] || {
	echo "review the rounding patch before changing webrender $version" >&2; exit 1;
}
GOFLAGS='' GOWORK=off go mod download "$module@$version"
original=$(GOFLAGS='' GOWORK=off go list -m -f '{{.Dir}}' "$module")
expected=1970400b9eecd268ad63bd39370d23e948166443655993d43d35be46d3f2ee8d
printf '%s  %s/images/gradients.go\n' "$expected" "$original" | sha256sum -c -
# Refuse reuse, including symlinks: stale output must never silently win.
mkdir "$out"
cp -R "$original" "$out/webrender"
chmod -R u+w "$out/webrender"
patch --batch --fuzz=0 -d "$out/webrender" -p1 < patches/webrender-explicit-rounding.patch
cp go.mod "$out/crier.mod"
cp go.sum "$out/crier.sum"
GOFLAGS='' GOWORK=off go mod edit -modfile="$out/crier.mod" -replace="$module=$out/webrender"
resolved=$(GOFLAGS="-modfile=$out/crier.mod" GOWORK=off go list -m -f '{{.Dir}}' "$module")
[ "$resolved" = "$out/webrender" ] || { echo "unexpected renderer: $resolved" >&2; exit 1; }
sha256sum patches/webrender-explicit-rounding.patch "$out/webrender/images/gradients.go" > "$out/renderer.sha256"
printf '%s  %s/images/gradients.go\n' "$expected" "$original" | sha256sum -c -
echo "renderer prepared: GOFLAGS=-modfile=$out/crier.mod"
