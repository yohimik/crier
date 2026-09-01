#!/bin/sh
# What `crier ping` runs for this platform: a read-only call that proves the
# credentials work without posting anything.
set -eu

: "${WEBHOOK_URL:?set WEBHOOK_URL, or put it in publish.custom.webhook.env}"

reply="$(mktemp)"
trap 'rm -f "$reply"' EXIT

curl -sS --fail-with-body "$WEBHOOK_URL" -o "$reply"

if command -v jq >/dev/null 2>&1; then
  echo "id=$(jq -r '.id // empty' "$reply")" >> "$CRIER_OUTPUT"
  echo "name=$(jq -r '.name // empty' "$reply")" >> "$CRIER_OUTPUT"
fi
