#!/bin/sh
# Announce a crier release on Instagram, with crier.
#
# dispat runs this from the announce stage, which only ever warns — so every
# reason not to post is a message and an exit 0, never a failed release. A
# missing secret must not turn a good release into a red build.
#
# One card and one clip per release, posted three ways: the feed carousel at
# 1080x1080, the anthem as a story, then the changelog pages as stories fitted
# into 1080x1920.
#
# The clip is rendered first, once, and used twice. It opens the feed carousel
# as its lead video and it opens the story reel as the first story. Both
# surfaces want the same sixteen seconds, and encoding them separately would
# spend a minute of a release producing a file crier already has.
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
frames_dir=""
cover_data=""
updates_data=""
anthem_dir=""
# anthem_mp4 is the rendered clip, once it exists. Empty means there is none,
# which is what every pass checks rather than assuming a file is there.
anthem_mp4=""
trap 'rm -rf "$data" "$frames_dir" "$cover_data" "$updates_data" "$anthem_dir"' EXIT
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

# --- the anthem ---------------------------------------------------------------
#
# The cover page, held for sixteen seconds with announce/anthem.mp3 as its
# soundtrack — the one way audio reaches Instagram, which takes no audio file
# and no track id (see docs/publishing/music.md and announce/anthem.md). The
# changelog is not in this clip: the image stories carry it, and this is the
# fanfare.
#
# It renders once and is posted twice, because both surfaces want the same
# sixteen seconds: the feed carousel opens with it, and the story reel opens
# with it. Encoding it a second time would burn a minute of a release to
# produce a file crier already has.
#
# The cover renders once and is copied into frames, because 384 identical
# layouts would cost minutes and one copied 384 times costs a second.
render_anthem() {
	command -v ffmpeg >/dev/null 2>&1 || {
		log "ffmpeg is not installed; the release goes out without the anthem"
		return 0
	}
	frames_dir=$(mktemp -d)
	cover_data=$(mktemp)
	anthem_dir=$(mktemp -d)
	frames=$frames_dir
	cover=$cover_data
	DISPAT_BREAKING_CHANGES='' DISPAT_FEATURES='' DISPAT_FIXES='' \
		sh "$here/notes.sh" >"$cover"
	if ! "$crier" render --config "$here/crier.yaml" --render-data - \
		--render-format png --render-background '#ffffff' \
		--render-output "$frames/cover.png" <"$cover"; then
		log "the cover did not render; the release goes out without the anthem"
		return 1
	fi
	i=0
	while [ "$i" -lt 384 ]; do
		i=$((i + 1))
		cp "$frames/cover.png" "$frames/$(printf 'f%03d' "$i").png"
	done
	rm -f "$frames/cover.png"

	log "rendering the anthem"
	if ! "$crier" render --config "$here/crier.yaml" --render-data - \
		--render-video-enabled=true \
		--render-video-fps 24 \
		--render-video-frames-input "$frames" \
		--render-video-audio "$here/anthem.mp3" \
		--render-background '#ffffff' \
		--render-output "$anthem_dir/anthem.mp4" <"$cover"; then
		log "the anthem did not render; the release goes out without it"
		return 1
	fi
	anthem_mp4=$anthem_dir/anthem.mp4
	log "rendered the anthem to $anthem_mp4"
	return 0
}

# --- the anthem story ---------------------------------------------------------
#
# The clip render_anthem already made, published as a story. No second render:
# publish.input takes the file as it stands.
anthem_story() {
	[ -n "$anthem_mp4" ] || return 0
	log "posting the anthem story"
	# The cover's data still goes in on standard input. The card is not
	# rendered in this mode, but the caption template is still resolved, and
	# the config points render.data at stdin.
	if "$crier" --config "$here/crier.yaml" --render-data - \
		--publish-input "$anthem_mp4" \
		--publish-instagram-story \
		--publish-instagram-width 1080 \
		--publish-instagram-height 1920 \
		--publish-instagram-fit contain \
		--publish-instagram-fit-background "#04140c" <"$cover_data"; then
		log "posted the anthem story"
		return 0
	fi
	log "the anthem story did not post; see the log above"
	return 1
}

# The clip is made before anything is posted, because the feed post opens with
# it: every post row leads with the video that carries the music.
#
# The reel reads in posting order: the anthem video is the cover story, the
# changelog pages follow it as pictures. The picture cover would only repeat
# what the video already shows, so the story pass strips it.
failures=0
render_anthem || failures=$((failures + 1))
if [ -n "$anthem_mp4" ]; then
	post "feed post" --publish-instagram-lead-video "$anthem_mp4" || failures=$((failures + 1))
else
	post "feed post" || failures=$((failures + 1))
fi
anthem_story || failures=$((failures + 1))
if [ -z "$(printf '%s' "${DISPAT_BREAKING_CHANGES:-}${DISPAT_FEATURES:-}${DISPAT_FIXES:-}" | tr -d '[:space:]')" ]; then
	log "no changelog pages; the cover video is the whole story"
else
	updates=$(mktemp)
	updates_data=$updates
	ANNOUNCE_NO_COVER=1 sh "$here/notes.sh" >"$updates"
	post_updates() {
		log "posting the stories"
		if "$crier" --config "$here/crier.yaml" --render-data - \
			--publish-instagram-story \
			--publish-instagram-width 1080 \
			--publish-instagram-height 1920 \
			--publish-instagram-fit contain \
			--publish-instagram-fit-background "#04140c" <"$updates"; then
			log "posted the stories"
			return 0
		fi
		log "the stories did not post; see the log above"
		return 1
	}
	post_updates || failures=$((failures + 1))
fi

if [ "$failures" -gt 0 ]; then
	# Four things can go wrong now rather than three: the anthem is rendered
	# once and then posted twice, so its render is a step of its own.
	log "$failures of the announcement's steps did not go out; the release itself is unaffected"
fi
exit 0
