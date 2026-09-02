#!/bin/sh
# Build the announcement card's data document from a dispat release.
#
# It reads the release-notes variables dispat gives every stage and writes one
# JSON object on standard output. It is a script of its own rather than a
# function inside announce.sh so that it can be run and checked without a
# release, a network, or a binary:
#
#   DISPAT_NEW_VERSION=1.2.3 DISPAT_FEATURES="a
#   b" sh announce/notes.sh
#
# The variables are documented in dispat's reference/environment.md: entries
# are one per line, in history order, and a group with no entries is set to
# empty text rather than unset.
set -eu

version=${DISPAT_NEW_VERSION:-dev}

# How many entries a section shows before it says how many are left.
#
# The card paginates, so the changelog no longer has to fit in one thumbnail:
# a long release becomes a carousel and the entries carry on across its pages.
# This is therefore a ceiling on absurdity rather than a design constraint. A
# release with sixty entries in one section is a release nobody is going to
# read to the end of, and it also has to stay under render.pages-max, which
# refuses the render outright rather than truncating it.
max=${ANNOUNCE_MAX_ITEMS:-20}

# escape makes one line safe to put inside a JSON string.
#
# Backslash first, or it would escape the backslashes the later rules add. Tab
# and carriage return are spelled out because a raw control character inside a
# JSON string is invalid, and release subjects have been known to carry both.
escape() {
	sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e 's/\t/\\t/g' -e 's/\r//g'
}

# section prints one {"label":…,"items":[…],"more":N} object, or nothing at all
# when the group is empty. An empty section is omitted rather than rendered
# blank: a card with a FIXES heading and no fixes under it looks broken.
section() {
	label=$1
	body=$2

	# Blank lines are dropped rather than counted: dispat separates entries
	# with newlines, and a here-doc or a trailing newline leaves an empty one.
	items=$(printf '%s\n' "$body" | grep -v '^[[:space:]]*$' || true)
	[ -n "$items" ] || return 0

	total=$(printf '%s\n' "$items" | wc -l | tr -d ' ')
	shown=$total
	[ "$shown" -le "$max" ] || shown=$max
	more=$((total - shown))

	printf '{"label":"%s","items":[' "$label"
	n=0
	printf '%s\n' "$items" | head -n "$shown" | while IFS= read -r line; do
		n=$((n + 1))
		[ "$n" -eq 1 ] || printf ','
		printf '"%s"' "$(printf '%s' "$line" | escape)"
	done
	printf '],"more":%d}' "$more"
}

# The three groups, in the order the changelog and the GitHub release use.
sections=""
for pair in "BREAKING:${DISPAT_BREAKING_CHANGES:-}" \
	"FEATURES:${DISPAT_FEATURES:-}" \
	"FIXES:${DISPAT_FIXES:-}"; do
	label=${pair%%:*}
	body=${pair#*:}
	one=$(section "$label" "$body")
	[ -n "$one" ] || continue
	[ -z "$sections" ] || sections="$sections,"
	sections="$sections$one"
done

esc_version=$(printf '%s' "$version" | escape)

# The install commands are built here rather than in the template so that the
# template stays a layout and the commands stay testable. All three routes the
# README documents, each pinned to the version being announced.
printf '{'
# ANNOUNCE_NO_COVER strips the cover from the render: the story pass posts
# the changelog pages alone, because the cover story is the anthem video.
[ -z "${ANNOUNCE_NO_COVER:-}" ] || printf '"nocover":true,'
# A graduation is distinguishable from an ordinary release: dispat hands the
# stage both channels, and rc to stable is the crossing this card dresses up
# for. The candidates count comes from the old version's counter, plus one
# because the train starts at rc.0. dispat's changelog collects the whole
# train into this release's sections, so the graduation card already gathers
# everything the candidates said one by one.
# Always present rather than only when true: the caption templates run with
# missingkey=error, and a key that only exists on graduation day would fail
# every ordinary release.
if [ "${DISPAT_OLD_CHANNEL:-}" = "rc" ] && [ "${DISPAT_CHANNEL:-}" = "stable" ]; then
	printf '"graduated":true,'
	counter=${DISPAT_OLD_VERSION##*rc.}
	case $counter in
	'' | *[!0-9]*) printf '"candidates":0,' ;;
	*) printf '"candidates":%d,' "$((counter + 1))" ;;
	esac
else
	printf '"graduated":false,'
fi
printf '"version":"%s",' "$esc_version"
printf '"sections":[%s],' "$sections"
printf '"install":['
printf '{"label":"curl","command":"curl -fsSL https://raw.githubusercontent.com/yohimik/crier/v%s/install.sh | CRIER_VERSION=%s sh"},' \
	"$esc_version" "$esc_version"
printf '{"label":"dispat","command":"dispat install yohimik/crier"},'
printf '{"label":"go","command":"go install github.com/yohimik/crier/cmd/crier@v%s"}' "$esc_version"
printf ']}'
printf '\n'
