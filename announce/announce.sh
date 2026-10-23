#!/bin/sh
# Instagram: cover clip, feed images, then cover and changelog stories.
# LinkedIn: one text-and-images post. Uses only existing crier options.
set -eu

root=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
here=$root/announce
log() { printf 'announce: %s\n' "$*" >&2; }

version=${DISPAT_NEW_VERSION:-}
if [ -z "$version" ]; then
	log "no DISPAT_NEW_VERSION, so there is no release to announce; skipping"
	exit 0
fi

seed=$(printf '%s' "$version" | cksum | cut -d' ' -f1)
log "seed for v$version is $seed"

only=${ANNOUNCE_ONLY:-}

missing=""
if [ "$only" != "linkedin" ]; then
	[ -n "${CRIER_PUBLISH_INSTAGRAM_TOKEN:-}" ] || missing="$missing CRIER_PUBLISH_INSTAGRAM_TOKEN"
	[ -n "${CRIER_PUBLISH_INSTAGRAM_USER_ID:-}" ] || missing="$missing CRIER_PUBLISH_INSTAGRAM_USER_ID"
fi

stage_mode=${CRIER_STAGE_MODE:-server}
[ "$only" != "linkedin" ] || stage_mode=none
if [ "$only" != "linkedin" ] && [ "$stage_mode" = "server" ] && [ -z "${NGROK_AUTHTOKEN:-}" ]; then
	missing="$missing NGROK_AUTHTOKEN"
fi

if [ -n "$missing" ]; then
	log "not announcing v$version: no$(printf '%s' "$missing" | sed 's/ / /g')"
	log "set them as repository secrets, or set CRIER_STAGE_MODE to stage without a tunnel"
	exit 0
fi

crier=${ANNOUNCE_CRIER_BIN:-$root/cmd/crier/dist/crier-linux-amd64}
if [ ! -x "$crier" ]; then
	log "no built binary at $crier; skipping (the build stage produces it)"
	exit 0
fi
log "announcing v$version with $crier"

data=$(mktemp)
frames_dir=""
cover_data=""
updates_data=""
anthem_dir=""
anthem_mp4=""
trap 'rm -rf "$data" "$frames_dir" "$cover_data" "$updates_data" "$anthem_dir"' EXIT
sh "$here/notes.sh" >"$data"

if [ "$only" != "linkedin" ] && [ "$stage_mode" = "server" ]; then
	if ! ngrok config add-authtoken "$NGROK_AUTHTOKEN" >/dev/null 2>&1; then
		log "could not write the ngrok authtoken; skipping"
		exit 0
	fi
	CRIER_STAGE_SERVER_TUNNEL_MODE=${CRIER_STAGE_SERVER_TUNNEL_MODE:-ngrok}
	export CRIER_STAGE_SERVER_TUNNEL_MODE
fi
CRIER_STAGE_MODE=$stage_mode
export CRIER_STAGE_MODE

post() {
	what=$1
	doc=$2
	shift 2
	log "posting the $what"
	if "$crier" --config "$here/crier.yaml" --render-data - --render-seed "$seed" "$@" <"$doc"; then
		log "posted the $what"
		return 0
	fi
	log "the $what did not post; see the log above"
	return 1
}

render_anthem() {
	command -v ffmpeg >/dev/null 2>&1 || {
		log "ffmpeg is not installed; the release goes out without the anthem"
		return 0
	}
	frames_dir=$(mktemp -d)
	cover_data=$(mktemp)
	anthem_dir=$(mktemp -d)
	cover=$cover_data
	DISPAT_BREAKING_CHANGES='' DISPAT_FEATURES='' DISPAT_FIXES='' \
		sh "$here/notes.sh" >"$cover"
	pages=$frames_dir/pages
	mkdir "$pages"
	if ! "$crier" render --config "$here/crier.yaml" --render-data - \
		--render-seed "$seed" \
		--render-format png --render-background '#ffffff' \
		--render-output "$pages/page.png" <"$data"; then
		log "the card did not render; the release goes out without the anthem"
		return 1
	fi
	front=$pages/page-1.png
	[ -f "$front" ] || front=$pages/page.png

	encode() {
		in=$1
		out=$2
		shift 2
		"$crier" render --config "$here/crier.yaml" --render-data - \
			--render-seed "$seed" \
			--render-video-enabled=true \
			--render-video-fps 24 \
			--render-video-frames-input "$in" \
			--render-background '#ffffff' \
			"$@" \
			--render-output "$out" <"$cover"
	}

	frames=$frames_dir/anthem-frames
	mkdir "$frames"
	i=0
	while [ "$i" -lt 384 ]; do
		i=$((i + 1))
		cp "$front" "$frames/$(printf 'f%03d' "$i").png"
	done
	log "rendering the anthem"
	if ! encode "$frames" "$anthem_dir/anthem.mp4"; then
		log "the anthem did not render; the release goes out without it"
		return 1
	fi
	anthem_mp4=$anthem_dir/anthem.mp4
	log "rendered the anthem to $anthem_mp4"

	return 0
}

anthem_story() {
	[ -n "$anthem_mp4" ] || return 0
	log "posting the anthem story"
	if "$crier" --config "$here/crier.yaml" --render-data - \
		--render-seed "$seed" \
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

failures=0
[ "$only" = linkedin ] || render_anthem || failures=$((failures + 1))

has_changelog=1
[ -n "$(printf '%s' "${DISPAT_BREAKING_CHANGES:-}${DISPAT_FEATURES:-}${DISPAT_FIXES:-}" | tr -d '[:space:]')" ] || has_changelog=0
updates=$data
if [ "$has_changelog" = 1 ]; then
	updates=$(mktemp)
	updates_data=$updates
	ANNOUNCE_NO_COVER=1 sh "$here/notes.sh" >"$updates"
fi

if [ "$only" = "linkedin" ]; then
	log "ANNOUNCE_ONLY=linkedin: the instagram passes stay quiet"
elif [ -n "$anthem_mp4" ] && [ "$has_changelog" = 1 ]; then
	post "feed post" "$updates" --publish-instagram-lead-video "$anthem_mp4" || failures=$((failures + 1))
elif [ -n "$anthem_mp4" ]; then
	post "feed post" "$cover_data" --publish-input "$anthem_mp4" || failures=$((failures + 1))
else
	post "feed post" "$data" || failures=$((failures + 1))
fi
[ "$only" = "linkedin" ] || anthem_story || failures=$((failures + 1))
if [ "$only" = "linkedin" ]; then
	: # the stories are instagram's
elif [ "$has_changelog" = 0 ]; then
	log "no changelog pages; the cover video is the whole story"
else
	post_updates() {
		log "posting the stories"
		if "$crier" --config "$here/crier.yaml" --render-data - \
			--render-seed "$seed" \
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

linkedin_post() {
	if [ -z "${CRIER_PUBLISH_LINKEDIN_TOKEN:-}" ] || [ -z "${CRIER_PUBLISH_LINKEDIN_AUTHOR_URN:-}" ]; then
		log "no CRIER_PUBLISH_LINKEDIN_TOKEN or CRIER_PUBLISH_LINKEDIN_AUTHOR_URN; the release goes out without the linkedin post"
		return 0
	fi
	post "linkedin post" "$data" --publish-instagram-enabled=false \
		--publish-linkedin-enabled=true --publish-input "" --stage-mode none
}
linkedin_post || failures=$((failures + 1))

if [ "$failures" -gt 0 ]; then
	log "$failures of the announcement's steps did not go out; the release itself is unaffected"
	[ "$only" != "linkedin" ] || exit 1
fi
exit 0
