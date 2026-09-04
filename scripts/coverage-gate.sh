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
# internal/template/exec is held lower because a third of it is type switches
# over slice and map types a data document rarely produces, each a line that
# exists so a value of that type is handled rather than refused.
# internal/raster is held lower on purpose: it is a drawing backend whose
# correctness is shown by the golden images rather than by exercising every
# branch of every blend mode. internal/selfupdate is held lower because half
# its branches are the Windows rename paths, which no test on this platform
# reaches.
FLOORS="
internal/app	93
internal/config	94
internal/configgen	94
internal/httpx	95
internal/logging	95
internal/procutil	91
internal/publish	93
internal/selfupdate	82
internal/stage	91
internal/template	96
internal/template/exec	70
internal/render	90
internal/raster	83
"

fail=0
printf '%-24s %8s %8s\n' PACKAGE COVERAGE FLOOR >&2
echo "$FLOORS" | while IFS='	' read -r pkg floor; do
	[ -n "$pkg" ] || continue
	# The package's own files only: a package's floor must not be blended
	# with a subpackage's, which the exec package under internal/template
	# would otherwise be.
	pct=$(go tool cover -func="$PROFILE" |
		grep "github.com/yohimik/crier/$pkg/[^/]*\.go:" |
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
