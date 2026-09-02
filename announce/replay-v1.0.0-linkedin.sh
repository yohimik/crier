#!/bin/sh
# Replays the v1.0.0 graduation announcement, LinkedIn only.
# Wants: CRIER_PUBLISH_LINKEDIN_TOKEN and CRIER_PUBLISH_LINKEDIN_AUTHOR_URN in the
# environment, ffmpeg on PATH, and a Go toolchain. Nothing touches Instagram.
set -eu
cd "$(git rev-parse --show-toplevel)"
go build -o /tmp/crier-replay ./cmd/crier
export ANNOUNCE_CRIER_BIN=/tmp/crier-replay
export ANNOUNCE_ONLY=linkedin
export DISPAT_NEW_VERSION=1.0.0 DISPAT_OLD_VERSION=1.0.0-rc.17 DISPAT_OLD_CHANNEL=rc DISPAT_CHANNEL=stable
export DISPAT_BREAKING_CHANGES='finally graduated, thanks for not unsubscribing during dogfooding
rc train before 1.0.0
fail closed on anything unrecognised
add init and make version a flag
make publish the default command'
export DISPAT_FEATURES='a graduation dresses up and takes its time
linkedin gets the whole release in one post
the release pings before it posts
the release chooses its own look and its own fanfare
boosty is the fourteenth platform
youtube is the thirteenth platform
threads is the twelfth platform
the release announces itself on linkedin too
vk is the eleventh platform
a twentieth probe to round the parade off at a number worth a page break of its own
an eighteenth probe of comfortable two-line length, the kind most real subjects turn out to be
a sixteenth probe, generously proportioned in the manner of the eighth, invoking imaginary flags and fictitious platforms for no purpose but the three lines it will be given on the card
the release renders its anthem once and leads with it
the carousel and the album open with the clip
a thirteenth probe, medium length, with just enough said to wrap once
an eighth probe that describes an imaginary feature in the language of a real one, mentioning a configuration key that does not exist and a platform that never heard of it, purely to occupy three wrapped lines
a seventh, short probe
a sixth probe about nothing at all, phrased the way changelog entries actually arrive, with a clause after the comma that pushes it past the width of one line
a fifth probe, whose subject settles in at around one hundred and sixty characters so that it reliably wraps to a second line and reaches toward a third on the card
a post can name the clip that opens it
a fourth probe that stands in for the release notes nobody plans: written in one breath, trailing detail after detail, the kind of subject that a reviewer asks to be split and the author promises to split next time and never does
a probe whose subject describes, at deliberate and unhurried length, the way a paginated release card lets every one of these words survive onto the page where a single-line card would have cut them at the first ellipsis
the release opens its story with a fanfare
ping checks the audio file, and publish says when nothing carries it
three platforms carry the audio, each its own way
the configuration can name an audio file
WWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWW
the release announces itself as a carousel
the last three platforms, and where each number came from
the platforms that take several files now do
a page list becomes a sequence of posts
keep every page a document lays out into
add Slack as the tenth platform
make partial pipelines first-class
render and publish animated GIFs
add custom platforms backed by shell scripts
add self-update
add the ping command
add the example gallery with bundled fonts
add template pools, seeded randomisation and overflow goldens
add the CLI, pipeline and render variants
add nine platform publishers'
export DISPAT_FIXES='the graduation caption could not fit its own train
the card stops undercounting its platforms
linkedin commentary survives its own punctuation
a transient meta refusal of a status read is waited out
linkedin urns travel percent-encoded, and ping proves upload
the story example stops counting platforms
a refused linkedin clip costs the clip, not the platform
the image cover stays home when the video leads
a fitted platform reshapes the file it was handed
number every page of a coverless card
instagram wants media_type on a video carousel child
a nineteenth probe that wraps twice by design, padding its clause with qualifiers the way release notes do when nobody edits them down before the tag
a seventeenth probe recounting, in the past tense and with unnecessary chronology, the discovery of a bug that never existed and the weekend that was never lost to it
a fifteenth probe that closes the parade at a length calculated to wrap twice, so the last page of the card has some weight to carry as well
a fourteenth probe, one word longer than the tenth
a twelfth probe whose subject would have been two sentences anywhere else, joined here by a comma because commit subjects have no full stops to spare, and stretched to guarantee its third line
an eleventh probe carrying a middling subject that lands almost exactly on the wrap boundary of the card face
a tenth probe, terse
a ninth probe fixing nothing, at length: the sort of subject where the author explains the symptom, the cause and the cure all before the colon has any right to expect them
a third probe, short
a second probe, briefer than the first but still comfortably past the width of one card line, so the wrapped entries alternate with the short ones on the pages
a long entry wraps instead of losing its ending
Meta'\''s second costume for the same race, and ffmpeg for the runner
ask again where a refusal created nothing
green padding between the text and the page margin
ask again when Instagram says the media is not ready
the page margin is the template'\''s to choose
a number is the same number whichever door it came in
pin the install block to the release card'\''s bottom edge
say out loud that instagram stories carry no caption
announce on the host the Instagram-login token parses on
drain the output before reaping, with a bound
pick the right face from a font collection, and nine smaller raster fixes
open request bodies late, and find a poster for a clip from disk
report Instagram'\''s real link, take GIFs in custom platforms, cap TikTok uploads
keep credentials out of errors and logs, and bound uploads separately
reap the process before draining its output
scan fonts file by file so one bad font costs one font
check a clip against its own kind, not against video
render on a machine with no fonts installed'
sh announce/announce.sh
