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

# How many entries a section shows before it says how many are left. A release
# card is read in a thumbnail, so the honest number is small.
max=${ANNOUNCE_MAX_ITEMS:-3}

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

# The card pins its install block to the bottom edge, so the changes above
# share a fixed vertical budget. Three sections at three entries each spill
# into it; the budget is therefore spent by section count — one or two
# sections keep the full allowance, all three drop to two entries each, and
# "+N more" absorbs the rest. ANNOUNCE_MAX_ITEMS still overrides.
present=0
for body in "${DISPAT_BREAKING_CHANGES:-}" "${DISPAT_FEATURES:-}" "${DISPAT_FIXES:-}"; do
	[ -z "$(printf '%s' "$body" | tr -d '[:space:]')" ] || present=$((present + 1))
done
if [ -z "${ANNOUNCE_MAX_ITEMS:-}" ] && [ "$present" -ge 3 ]; then
	max=2
fi

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
printf '"version":"%s",' "$esc_version"
printf '"sections":[%s],' "$sections"
printf '"install":['
printf '{"label":"curl","command":"curl -fsSL https://raw.githubusercontent.com/yohimik/crier/v%s/install.sh | CRIER_VERSION=%s sh"},' \
	"$esc_version" "$esc_version"
printf '{"label":"dispat","command":"dispat install yohimik/crier --asset '"'"'crier-{os}-{arch}'"'"'"},'
printf '{"label":"go","command":"go install github.com/yohimik/crier/cmd/crier@v%s"}' "$esc_version"
printf ']}'
printf '\n'
