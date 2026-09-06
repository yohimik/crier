#!/bin/sh
# Build the release binaries and hand them to dispat.
#
# dispat runs this as the package's build step, with the working directory set
# to the package folder (cmd/crier) and the version in the environment. The
# work happens inside the root Dockerfile: the export target descends from the
# release-test target. Six standard Go binaries keep their existing names;
# crier-tiny-linux-amd64 and crier-tiny-linux-arm64 are additive, stripped
# assets. Native artifacts pass full E2E and pixel checks before export.
#
# The assets are bare, uncompressed binaries named crier-{goos}-{goarch}, which
# is dispat's own selfupdate convention: `dispat install yohimik/crier, install.sh
# and install.ps1 all resolve exactly these
# names. No archives and no checksum file — GitHub reports a sha256 digest per
# asset, and that is what all three verify against.
set -eu

ROOT=$(git rev-parse --show-toplevel)

rm -rf dist

# DISPAT_EXPORT_BASE is where the export is about to land, which is the one
# thing the container cannot work out for itself: it names its artefacts
# against that path, so what comes back needs no rewriting here.
# The optional token is a BuildKit secret, never a build argument.
# shellcheck disable=SC2086
docker buildx build \
	--file "$ROOT/Dockerfile" \
	--target export \
	--build-arg DISPAT_VERSION="${DISPAT_NEW_VERSION:-dev}" \
	--build-arg DISPAT_COMMIT="$(git rev-parse --short=12 HEAD)" \
	--build-arg DISPAT_EXPORT_BASE="$PWD/dist" \
	${GITHUB_TOKEN:+--secret id=GITHUB_TOKEN,env=GITHUB_TOKEN} \
	--output "type=local,dest=$PWD/dist" \
	"$ROOT"

# Already this stage's outputs, in $DISPAT_OUTPUT's own format.
cat dist/dispat-output >> "$DISPAT_OUTPUT"
rm -f dist/dispat-output
