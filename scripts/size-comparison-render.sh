#!/bin/sh
set -eu
root=$(CDPATH='' cd -- "$(dirname "$0")/.." && pwd)
cd "$root"
out="$root/coverage/size-comparison"
target=${1:-linux-arm64}
for compiler in ${SIZE_COMPARISON_COMPILERS:-go tinygo}; do
  bin="$out/bin/$compiler-$target"
  dest="${SIZE_COMPARISON_PIXEL_DIR:-$out/pixels}/$target/$compiler"
  mkdir -p "$dest"
  for fixture in square-1080 story-1080x1920 business-promo video-game-release social-quote release-changelog event-invite; do
    "$bin" render --config "examples/$fixture/crier.yaml" \
      --render-seed 12345 --render-output "$dest/$fixture.png"
  done
  "$bin" render --config examples/video-game-release/crier.yaml \
    --render-variant instagram --render-seed 12345 --render-output "$dest/video-game-story.png"
  for layout in template.html template-b.html; do
    mkdir -p "$dest/$layout"
    DISPAT_NEW_VERSION=1.2.3 DISPAT_FEATURES='Render one release with both compilers
Keep the same fonts and fixtures
Publish release media together' DISPAT_FIXES='Close the listener after use' \
      sh announce/notes.sh | "$bin" render --config announce/crier.yaml \
      --render-pool "$root/announce/$layout" --render-seed 12345 --render-data - \
      --render-format png --render-output "$dest/$layout/page.png"
  done
done
