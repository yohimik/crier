#!/bin/sh
# Post the release card, from inside a dispat release.
#
# dispat runs its publish scripts with the release in the environment, so the
# card can be built from it without anybody editing a file:
#
#   DISPAT_PACKAGE, DISPAT_NEW_VERSION,
#   DISPAT_FEATURES, DISPAT_FIXES, DISPAT_BREAKING_CHANGES
#
# Wire it into dispat.yaml as one more publish step:
#
#   packages:
#     crier:
#       flow:
#         publish: [changelog, commit, github, announce]
#   scripts:
#     announce: sh examples/release-changelog/announce.sh
set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

# crier reads its data document from standard input when render.data is "-",
# so the release becomes a card without a temporary file.
cat <<YAML | crier publish --config "$here/crier.yaml" --render-data -
package: ${DISPAT_PACKAGE:-crier}
version: ${DISPAT_NEW_VERSION:-dev}
features: |
  ${DISPAT_FEATURES:-}
fixes: |
  ${DISPAT_FIXES:-}
breaking: |
  ${DISPAT_BREAKING_CHANGES:-}
footer: github.com/yohimik/crier
YAML
