#!/bin/sh
# A custom platform: post the rendered card to a webhook with curl.
#
# crier runs this with `sh -c`, so everything it needs arrives in the
# environment. See docs/publishing/custom.md for the full contract.
set -eu

: "${WEBHOOK_URL:?set WEBHOOK_URL, or put it in publish.custom.webhook.env}"

# CRIER_ARTIFACT is the rendered file, CRIER_CAPTION the post text.
reply="$(mktemp)"
trap 'rm -f "$reply"' EXIT

curl -sS --fail-with-body -X POST "$WEBHOOK_URL" \
  -F "file=@${CRIER_ARTIFACT}" \
  -F "payload_json={\"content\": $(printf '%s' "$CRIER_CAPTION" | sed 's/\\/\\\\/g; s/"/\\"/g; s/^/"/; s/$/"/')}" \
  -o "$reply"

# Appending to CRIER_OUTPUT is how a script reports what it published. It is
# optional: exiting 0 is the claim that the post went out.
if command -v jq >/dev/null 2>&1; then
  id="$(jq -r '.id // empty' "$reply")"
  [ -n "$id" ] && echo "id=$id" >> "$CRIER_OUTPUT"
fi

echo "posted $CRIER_ARTIFACT as $CRIER_PLATFORM" >&2
