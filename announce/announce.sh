#!/bin/sh
# Announce a crier release on Instagram, with crier.
#
# dispat runs this from the announce stage, which only ever warns — so every
# reason not to post is a message and an exit 0, never a failed release. A
# missing secret must not turn a good release into a red build.
#
# Two passes per release from one card: the feed post at 1080x1080, then the
# story, which is the same render fitted into 1080x1920.
#
# The card paginates. A release with a long changelog lays out into several
# pages, and each pass turns those into what its surface takes: the feed pass
# posts them as one carousel, the story pass as one story per page in order.
# Neither is a flag here — crier works it out from the platform.
#
# The binary is the one the release just built. That is the point: the bytes
# being announced are the bytes doing the announcing, so a release that cannot
# render its own card does not ship.
set -eu

root=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
here=$root/announce
log() { printf 'announce: %s\n' "$*" >&2; }

version=${DISPAT_NEW_VERSION:-}
if [ -z "$version" ]; then
	log "no DISPAT_NEW_VERSION, so there is no release to announce; skipping"
	exit 0
fi

# --- what has to be there -----------------------------------------------------
#
# Collected rather than checked one at a time, so somebody setting this up is
# told everything that is missing at once instead of one secret per release.
missing=""
[ -n "${CRIER_PUBLISH_INSTAGRAM_TOKEN:-}" ] || missing="$missing CRIER_PUBLISH_INSTAGRAM_TOKEN"
[ -n "${CRIER_PUBLISH_INSTAGRAM_USER_ID:-}" ] || missing="$missing CRIER_PUBLISH_INSTAGRAM_USER_ID"

# The tunnel is only needed when crier is staging the file itself. Pointing
# CRIER_STAGE_MODE at s3 or at a URL somebody else publishes skips it, which is
# the escape hatch for anyone who would rather not run a tunnel in CI.
stage_mode=${CRIER_STAGE_MODE:-server}
if [ "$stage_mode" = "server" ] && [ -z "${NGROK_AUTHTOKEN:-}" ]; then
	missing="$missing NGROK_AUTHTOKEN"
fi

if [ -n "$missing" ]; then
	log "not announcing v$version: no$(printf '%s' "$missing" | sed 's/ / /g')"
	log "set them as repository secrets, or set CRIER_STAGE_MODE to stage without a tunnel"
	exit 0
fi

# --- the binary the release built ---------------------------------------------
crier=${ANNOUNCE_CRIER_BIN:-$root/cmd/crier/dist/crier-linux-amd64}
if [ ! -x "$crier" ]; then
	log "no built binary at $crier; skipping (the build stage produces it)"
	exit 0
fi
log "announcing v$version with $crier"

# --- the card's data ----------------------------------------------------------
#
# Written once and reused for both passes, so the story is the same card as the
# feed post rather than a second render of a moving target.
data=$(mktemp)
trap 'rm -f "$data"' EXIT
sh "$here/notes.sh" >"$data"

# --- staging ------------------------------------------------------------------
#
# Instagram fetches the media from a public URL of its own accord, and a runner
# has none. ngrok gives the stage server one for the length of the run.
if [ "$stage_mode" = "server" ]; then
	# Idempotent: writing the same token again is not an error, and the agent
	# reads its config file rather than the environment.
	if ! ngrok config add-authtoken "$NGROK_AUTHTOKEN" >/dev/null 2>&1; then
		log "could not write the ngrok authtoken; skipping"
		exit 0
	fi
	CRIER_STAGE_SERVER_TUNNEL_MODE=${CRIER_STAGE_SERVER_TUNNEL_MODE:-ngrok}
	export CRIER_STAGE_SERVER_TUNNEL_MODE
fi
CRIER_STAGE_MODE=$stage_mode
export CRIER_STAGE_MODE

# --- post ---------------------------------------------------------------------
#
# The feed post is the card as the config draws it. The story is the same card
# fitted into 1080x1920, which is a set of flags rather than a second config:
# one file describing one card is easier to keep true than two.
#
# Every page goes out either way. A story sequence has no cover-page opt-out:
# posting page one and dropping the changelog would be announcing a release
# without saying what is in it.
post() {
	what=$1
	shift
	log "posting the $what"
	if "$crier" --config "$here/crier.yaml" --render-data - "$@" <"$data"; then
		log "posted the $what"
		return 0
	fi
	# One post failing must not take the other with it, and neither may fail
	# the release.
	log "the $what did not post; see the log above"
	return 1
}

failures=0
post "feed post" || failures=$((failures + 1))
post "stories" \
	--publish-instagram-story \
	--publish-instagram-width 1080 \
	--publish-instagram-height 1920 \
	--publish-instagram-fit contain \
	--publish-instagram-fit-background "#04140c" ||
	failures=$((failures + 1))

if [ "$failures" -gt 0 ]; then
	log "$failures of 2 passes did not go out; the release itself is unaffected"
fi
exit 0
