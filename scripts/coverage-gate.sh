#!/bin/sh
# Fail when the business-logic packages fall below their coverage floor.
#
# The floor is per package rather than one number for the repository: a big
# well-covered package would otherwise hide a small uncovered one, which is
# exactly the direction coverage numbers drift.
set -eu

PROFILE="${1:-coverage/profile.txt}"
[ -f "$PROFILE" ] || { echo "no coverage profile at $PROFILE" >&2; exit 1; }

# package<TAB>floor, each a couple of points under what the suite currently
# reaches, so an ordinary change does not fail the gate and a package losing
# its tests does.
#
# internal/raster and internal/render are held lower on purpose: they are a
# drawing backend whose correctness is shown by the golden images rather than
# by exercising every branch of every blend mode.
FLOORS="
internal/app	88
internal/config	95
internal/configgen	94
internal/httpx	90
internal/logging	95
internal/procutil	88
internal/publish	88
internal/selfupdate	82
internal/stage	90
internal/template	95
internal/render	82
internal/raster	80
"

fail=0
printf '%-24s %8s %8s\n' PACKAGE COVERAGE FLOOR >&2
echo "$FLOORS" | while IFS='	' read -r pkg floor; do
	[ -n "$pkg" ] || continue
	pct=$(go tool cover -func="$PROFILE" |
		grep "github.com/yohimik/crier/$pkg/" |
		awk '{ gsub(/%/, "", $NF); total += $NF; n++ } END { if (n) printf "%.1f", total / n; else print "0" }')
	printf '%-24s %7s%% %7s%%\n' "$pkg" "$pct" "$floor" >&2
	awk -v got="$pct" -v want="$floor" 'BEGIN { exit (got + 0 >= want + 0) ? 0 : 1 }' || {
		echo "  $pkg is below its coverage floor" >&2
		fail=1
	}
	# The loop body runs in a subshell, so the verdict travels through a file.
	[ "$fail" -eq 0 ] || touch "$PROFILE.failed"
done

if [ -f "$PROFILE.failed" ]; then
	rm -f "$PROFILE.failed"
	exit 1
fi
