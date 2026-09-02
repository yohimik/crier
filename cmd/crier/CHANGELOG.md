# Changelog

## v1.0.0-rc.5 (2026-09-02)

### Features

- the release opens its story with a fanfare ([a5d3b9f](https://github.com/yohimik/crier/commit/a5d3b9f31ed63736fc5345d0a6ed19be23beb5de)) (by yohimik, Claude Fable 5)
  A third announce pass: the cover page held for sixteen seconds as a
  video story, with sixteen seconds of the 1812 Overture finale as its
  soundtrack — a public-domain recording by the United States Marine Band,
  provenance in announce/anthem.md. Video is the one way audio reaches
  Instagram, which takes no audio file and no track id. The cover renders
  once and is copied into frames: 384 identical layouts would cost
  minutes, one copied 384 times costs a second.

- ping checks the audio file, and publish says when nothing carries it ([79b8393](https://github.com/yohimik/crier/commit/79b83937ac281d528b8ee37df1cf410f15edb676)) (by yohimik, Claude Fable 5)
  A row per configured music file: the file is there, it can be read, and
  its first bytes are one of the four containers crier sends. The extension
  is not consulted, because an image renamed to .mp3 is accepted by the
  file system and refused by the platform, after the post.

  The rows come before the publishers are built, since building one refuses
  a file that is not audio — a check that ran after the build would never
  be reached by the configuration it exists to explain.

  A file no enabled platform can carry is a success with a note rather than
  a failure. The file is fine. Publishing warns about the same finding, so
  a run that enables Instagram and X and names a jingle says out loud that
  the jingle is going nowhere.

- three platforms carry the audio, each its own way ([4668bee](https://github.com/yohimik/crier/commit/4668bee7a454431a54f7462cea040eb79235ef67)) (by yohimik, Claude Fable 5)
  Discord and Slack put the track in the same message as the pictures.
  Discord takes it as another files[n] part; Slack gives it an upload slot
  like any other file, and step three shares them together, which is what
  makes them one message.

  The audio takes one of the message's file slots, so both declare one
  fewer page per post when music is configured. Reserving the slot is what
  lets a long document paginate into messages that fit, rather than into a
  message the platform refuses.

  Telegram cannot do that. The Bot API groups audio only with audio, so a
  track cannot join the album it belongs to. It goes out as its own
  message, immediately after, in the same chat, which is what clients
  render as a player under the pictures. That message failing is a warning
  rather than a failure: the post is already out, and saying the platform
  failed would be untrue.

  TikTok gets auto_add_music in post_info for a photo direct post. It is
  the only music setting any of these APIs offers, and it names no track.

- the configuration can name an audio file ([96b28ae](https://github.com/yohimik/crier/commit/96b28ae9b2178b4ef485d8ab611e647e5d3bc409)) (by yohimik, Claude Fable 5)
  publish.music-file names one audio file for the whole run, and
  publish.<platform>.music-file overrides it for one platform. Both anchor
  to the config file the way every other path key does.

  The per-platform key exists for all ten platforms rather than only for
  the three whose API can carry audio. A key that simply did not exist for
  Instagram would answer "can I attach a track here" with an unknown-key
  error, which reads like a typo; the key exists, its reference row says it
  is not available, and a value in it is refused with the reason. Only
  Discord, Slack and Telegram have an endpoint that takes an audio file at
  all.

  publish.tiktok.auto-add-music comes along beside it. It is the one music
  setting any of these APIs offers, and it names no track: TikTok picks
  one.

- WWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWW ([23fc4a2](https://github.com/yohimik/crier/commit/23fc4a2fe1caea805e46cc3b25e1664f39a9c271)) (by yohimik, Claude Fable 5)
  A deliberate hostile-changelog probe, and nothing else: this subject is
  a hundred unbroken copies of the widest glyph the card's face carries,
  here so the next release candidate has to ellipsise it on its own card,
  in production, where the smoke suite's copy of the same monster already
  passes. A changelog is what people wrote; the card has to survive that.

### Fixes

- ask again where a refusal created nothing ([5469a24](https://github.com/yohimik/crier/commit/5469a243a8bdfd1f3d74a4d62b9cff47643edd5b)) (by yohimik, Claude Fable 5)
  Three platforms share Instagram's race between media processed and media
  usable, and each now gets the treatment its own API earns:

  - Mastodon refuses a status whose attachment is still processing with a
    422 raised before any status exists; it is retried within the poll
    budget. The message is localized per account, so a non-English
    instance falls back to the never-repeat rule, which is the safe
    direction to miss in.
  - X answers a not-yet-consistent media id with the same 400 it uses for
    a genuinely wrong one, and neither creates a tweet; the bounded retry
    costs a permanent mistake the poll budget and then surfaces it
    unchanged. Two new keys carry the budget.
  - LinkedIn is the dangerous one: the post can be accepted with 201, sit
    in PUBLISH_REQUESTED, and end PUBLISH_FAILED with nothing visible and
    no error on any call crier made. Images now wait for AVAILABLE before
    the post, the way videos always did.
  - Reddit is the inverse and gets no retry at all: its failure signal can
    arrive for a post that was nonetheless created.
  - Slack blocks until ingestion finishes; TikTok fails inside its own
    polled flow; Facebook photos are synchronous. Nothing to do on any of
    the three.

### Authors

- yohimik
- Claude Fable 5


## v1.0.0-rc.4 (2026-09-02)

### Fixes

- green padding between the text and the page margin ([13d5a50](https://github.com/yohimik/crier/commit/13d5a5062348ff92d17976e57e0519c6dd3f8c68)) (by yohimik, Claude Fable 5)
  Type sat flush against the white frame on every page. The panel now
  extends past the text, and the cover height subtracts the padding it
  gained.

- ask again when Instagram says the media is not ready ([8dc5170](https://github.com/yohimik/crier/commit/8dc5170416649c4b226dd92b8ccf07d904ae7044)) (by yohimik, Claude Fable 5)
  A container can poll FINISHED while Meta is still making the media
  publishable, and media_publish then answers error 9007, media not
  available. That refusal means no post was created, so it is the one
  publish failure that is safe to retry, bounded by the poll budget. Seen
  on the rc.3 release: the carousel published and the first story,
  seconds behind it, was told to wait a moment. Every other publish
  failure keeps the never-repeat rule.

### Authors

- yohimik
- Claude Fable 5


## v1.0.0-rc.3 (2026-09-02)

### Features

- the release announces itself as a carousel ([34535f2](https://github.com/yohimik/crier/commit/34535f22d56798cc8f0dc7b6e3ff838b468112ff)) (by yohimik, Claude Fable 5)
  The card was one page, and a changelog was cut to two or three entries a
  section to make it fit. That was the constraint the whole card was designed
  around, and it is gone.

  Page one is the cover: the version badge a thumbnail has to carry, the lede,
  and the three install routes pinned to the bottom edge. The changelog carries
  on across the pages after it, under a small version badge and a footer that
  numbers the page. Both are drawn in the page margins, so they repeat without
  being written into the flow, and the first page suppresses them because it
  carries its own badge and because "1 / 1" on a short release says nothing.

  Each pass turns those pages into what its surface takes. The feed post is one
  carousel. The story pass posts one story per page, in order, each live before
  the next is created. Neither is a flag: crier works it out from the platform.
  Stories have no cover-page opt-out — announcing a release without saying what
  is in it is not a saving worth making.

  notes.sh keeps a ceiling of twenty entries a section, which is a guard against
  absurdity rather than a design constraint: past it the render would exceed
  render.pages-max and be refused outright, and a release with sixty entries in
  one section is not one anybody reads to the end of.

  The end-to-end test now asserts the whole shape against a fake Graph API: a
  child container per page, one parent listing them in order, one publish, and
  then a story per page. The committed preview is both pages.

- the last three platforms, and where each number came from ([1ddab98](https://github.com/yohimik/crier/commit/1ddab98be19e959cf2bf613c828b1704e81c538f)) (by yohimik, Claude Fable 5)
  LinkedIn takes two to twenty as a multi-image post. One image is a different
  shape entirely — content.media rather than content.multiImage, and a
  multiImage of one is refused — so the shape follows the batch size rather than
  a flag. TikTok's photo post was already a list of URLs, so several pages are
  more entries in the request it always sent, up to the documented thirty-five.

  Reddit stays at one file per post on purpose. It has galleries, but the only
  way to make one is an endpoint Reddit's own web client uses and Reddit
  documents nowhere, with no published limit and no promise it keeps working.
  Several ordinary posts that will still be there next month beat one gallery
  built on that.

  The numbers are now sorted into the ones a platform documents and the ones
  crier chose. Instagram's ten, Telegram's two-to-ten, x's four, LinkedIn's
  twenty and TikTok's thirty-five are the platform's own words. Facebook,
  Discord and Slack document the mechanism and no limit on it, so ten is crier's
  ceiling rather than theirs, and the comments say so. Mastodon's four is the
  instance's, advertised as max_media_attachments and merely defaulted to four,
  so crier assumes the conservative number.

  One test lists every capacity with the source of each, so a number that
  changes without that list changing is a post that would silently start
  splitting.

- the platforms that take several files now do ([21f8ef6](https://github.com/yohimik/crier/commit/21f8ef6a412af7e61a09a0a1f5e3dec21a9a7955)) (by yohimik, Claude Fable 5)
  Seven of the ten accept more than one file in a post, and each does it
  differently. Every one of them keeps the page order.

  Instagram builds a carousel: a child container per image, a parent that lists
  them, one publish. The caption belongs to the parent, which is what makes a
  five-page changelog one entry in the feed rather than five. Stories have no
  carousel at all, so a story run stays at one file and becomes one story per
  page, published in order.

  Facebook uploads each photo unpublished and attaches the ids to one feed post.
  Telegram sends a media group, with the caption on the first item because
  Telegram shows an album's caption once; a batch of one still goes through
  sendPhoto, since a media group has a minimum of two. x and Mastodon upload in
  page order and list the ids. Discord numbers its file parts. Slack uploads each
  file and shares the lot in one completeUploadExternal, which is what makes them
  one message.

  A custom platform gets CRIER_ARTIFACTS and CRIER_URLS, one path per line
  because a path may contain a space, plus a count and the post and page numbers.
  Its capacity is whatever max-attachments says, since the command is the
  platform and there is no API here to know better.

  The e2e suite proves the three shapes against the fakes: sequential stories,
  one carousel, and a four-cap split. One scenario runs all three platforms
  together and asserts each saw the same five pages in the same order. It is in
  the smoke subset, so the release build proves it against the shipped bytes.

  LinkedIn, Reddit and TikTok still take one file each. They follow.

- a page list becomes a sequence of posts ([e1754f0](https://github.com/yohimik/crier/commit/e1754f0b7e86c9fc18b1f1afe921b09feab82112)) (by yohimik, Claude Fable 5)
  The run has one ordered page list. Every platform receives it whole, in order,
  and the only thing a platform may do to it is cut it into the sizes it accepts.
  Nothing reorders, skips, merges or dedupes pages. That is what makes a carousel
  at one platform and a run of single posts at another tell the same story in the
  same sequence.

  Needs.MaxAttachments declares how many files a platform takes in one post, with
  zero read as one. Every platform still declares one, so today a five-page
  document is five posts everywhere; raising the numbers and implementing the
  multi-file calls is the next commit. publish.<platform>.max-attachments lowers
  it, and only lowers it: asking a platform for more than it takes would only be
  refused by the platform, which is a worse way to find out.

  A platform's posts go out one at a time, each finished before the next begins.
  That is not caution. Several platforms order a feed by when a post completed,
  so publishing two of one sequence at once is how a two-part post turns up back
  to front. A post that fails stops the ones after it and the outcome says how
  far it got: a gap in the middle of a sequence is worse than a short sequence,
  because a reader cannot tell it happened. Platforms still run alongside each
  other; the sequencing is within one.

  Captions are rendered once per post rather than once per run, with .Post,
  .Posts, .Page and .Pages bound, so one line of configuration can write "2 of
  3". They read 1 of 1 when nothing paginated, so a caption that mentions them is
  safe to write either way.

- keep every page a document lays out into ([8c35002](https://github.com/yohimik/crier/commit/8c35002231ca0f7c0b471d088fc8f88755f9269a)) (by yohimik, Claude Fable 5)
  Laying out was always producing pages. crier kept the first and refused the
  rest, so a changelog too long for one card was an error telling you to shorten
  it.

  It is a post now. A run produces one ordered page list, and that list is what
  every platform receives: same pages, same order, everywhere. The fit is applied
  per page, so a carousel is a set of pictures the same shape rather than one
  fitted picture and four that were not. Staging gives each page an address of
  its own, because a platform that fetches by URL fetches every page.

  render.pages-max, default 10, refuses a document past it. The check runs after
  the layout, since the count is not knowable until the content has flowed: a
  template with a loop that never ends fails here rather than at the platform,
  which would have taken the first ten and said nothing about the rest. The hard
  ceiling is 20, above every platform's own carousel limit bar Reddit's.

  A document that still fits on one page produces one page with the file name it
  always had, so nothing downstream has to learn about pagination to keep
  working. `crier render` writes the whole set, numbered when there is more than
  one.

  Nothing publishes more than one page yet. That is the next commit.

### Fixes

- the page margin is the template's to choose ([fdd258c](https://github.com/yohimik/crier/commit/fdd258cdd14ea29165a21e52f275b5306ae8e676)) (by yohimik, Claude Fable 5)
  crier appended its page rule after the document's own styles, and that rule
  carried two declarations: the size and a zero margin. The size belongs there.
  Putting it last is what makes --render-width win over a rule the template
  happens to carry.

  The margin did not belong there. Appended last it overruled whatever margin the
  template asked for, and a page's margin boxes draw in the page margin. With the
  margin forced to zero they had no room: a running header and footer came out
  clipped to a sliver, straddling the page edge. A paginated template could not
  have worked, which is why this surfaced while verifying paged media at all.

  The rule is split across the document now. The margin goes first, as a default
  a template can overrule. The size still goes last, where it cannot be. A
  template that says nothing still gets an edge-to-edge page, because that is
  what a social image is.

  Verified through the raster backend: page margin boxes paint, the page counters
  resolve, the first-page selector picks page one, running headers from content
  work, and a card that would straddle a page break moves whole. A three-page
  golden pins the lot.

### Authors

- yohimik
- Claude Fable 5


## v1.0.0-rc.2 (2026-09-01)

### Fixes

- a number is the same number whichever door it came in ([0384c23](https://github.com/yohimik/crier/commit/0384c23b5a02936a079e15b40a32c9796b2647d6)) (by yohimik, Claude Fable 5)
  encoding/json hands every number over as float64 while the YAML decode
  keeps integers whole, so the same document type-checked differently by
  path: gt .count 0 worked from stdin and failed from count.json. The
  value now comes from the YAML decode for both, with the JSON parse kept
  for its error messages.

- pin the install block to the release card's bottom edge ([a20d0fd](https://github.com/yohimik/crier/commit/a20d0fd570f7107b1766d3269c12d78c6518e7cf)) (by yohimik, Claude Fable 5)
  A short changelist left it floating mid-card and a long one pushed it
  against the edge. Absolutely positioned rather than flexed, because the
  layout engine's flexbox is the weaker of the two — and the changes above
  now spend a section-count-aware budget, two entries each when all three
  sections are present, so they can never grow down into it.

### Authors

- yohimik
- Claude Fable 5


## v1.0.0-rc.1 (2026-09-01)

### Fixes

- say out loud that instagram stories carry no caption ([cacd49d](https://github.com/yohimik/crier/commit/cacd49d0a5643b53b29cd455dc13353bd26531c6)) (by yohimik, Claude Fable 5)
  The Stories API has no caption field and Meta ignores the parameter, so
  the announce story went out bare and the drop looked like a crier bug.
  The caption is now omitted with a warning that says where story text
  actually goes: into the image.

### Authors

- yohimik
- Claude Fable 5


## v1.0.0-rc.0 (2026-09-01)

### Breaking Changes

- rc train before 1.0.0 ([aad80fc](https://github.com/yohimik/crier/commit/aad80fceda738602ec6ec42fd88509493e377417)) (by yohimik)

- fail closed on anything unrecognised ([b5720f3](https://github.com/yohimik/crier/commit/b5720f329a5967fcba8018a17ca78acd6bde643e)) (by yohimik, Claude Fable 5)
  An unknown command word, an unknown flag anywhere, and a stray positional
  argument are all usage errors now, and the unknown-command message lists what
  would have worked. Nothing is guessed and nothing passes through: the failure
  this prevents is a mistyped --dry-run that publishes for real.

  Adds `--set key=value`, repeatable, which sets any key by its dotted name at
  the flag layer. A key that does not exist is refused with the nearest one
  suggested, because a flag that silently goes nowhere looks like it worked.

- add init and make version a flag ([57ead75](https://github.com/yohimik/crier/commit/57ead75738a6f3ecd0efb53de046e0e6c6e583e1)) (by yohimik, Claude Fable 5)
  `crier init` writes a configuration to start from: a short commented file by
  default, every option with `--full`, in yaml, json or toml. The generator moved
  into internal/configgen and is now shared with tools/gendocs, so what init
  writes and what crier.example.yaml holds come from one walk over the registry
  and cannot drift apart.

  `crier version` becomes `crier --version`, special-cased ahead of the rule that
  sends a leading flag to publish.

  The system font scan is wrapped in a recover: the parser underneath panics
  rather than erroring on a font file it cannot read, and one such file took the
  whole program down while rendering something that never needed that font.

- make publish the default command ([f615fd6](https://github.com/yohimik/crier/commit/f615fd6b8d9c4eb6eefe1110e1c2f4a68cd09a1a)) (by yohimik, Claude Fable 5)
  Bare `crier` renders and posts, so the everyday flow is to change into a
  project directory and run one word. Also silences the lint gate with
  reasons rather than blanket exclusions.

### Features

- add Slack as the tenth platform ([43f5265](https://github.com/yohimik/crier/commit/43f5265835b884c9406604b9a7cf86408bfc6d5c)) (by yohimik, Claude Fable 5)
  The modern external-upload flow, because the one-call method is gone:
  files.getUploadURLExternal hands out a slot, the raw bytes go to that URL
  unauthenticated — the URL is the credential — and files.completeUploadExternal
  says which channel and what to say. Only the third call posts, so only it is
  exempt from retry.

  Slack reports application errors inside a 200 with ok:false, so the status code
  alone says nothing; the envelope is read and the four failures worth explaining
  are translated, `not_in_channel` into the /invite that fixes it.

  Ping is auth.test, which needs no scope at all and so separates a token Slack
  never heard of from one that merely cannot do what crier wants. It reports the
  user and the workspace, and admits that it cannot confirm the bot is in the
  channel.

  Full peer treatment: fan-out, overlay and size keys, caption templating, ping,
  dry run, the GIF matrix, docs, the generated reference group, and e2e scenarios
  asserting the three-step linkage, the caption, the headers, and a bad token.

  Verified against Slack's method reference on 2026-09-01.

- make partial pipelines first-class ([a2fbb12](https://github.com/yohimik/crier/commit/a2fbb121cec0386b59790cec47e1253785785068)) (by yohimik, Claude Fable 5)
  The pipeline has four entrances rather than one. publish.input posts a file
  that already exists — identified by its bytes rather than its name, and
  transcoded png↔jpeg only when a platform insists, since re-encoding somebody's
  video to satisfy a preference would be a surprising thing for a publish command
  to do. render.video.frames-input encodes frames made anywhere else through the
  same ffmpeg pipeline, decoded one at a time and checked for a uniform size.

  The three paths converge on the same artifacts, so staging, format negotiation
  and the fan-out are unaware of which one ran. Naming two sources for the
  artifact is a config error: it is a configuration whose author believed two
  different things.

- render and publish animated GIFs ([e396563](https://github.com/yohimik/crier/commit/e396563a9fec871ad1110a2f79cefe5ca8c0a009)) (by yohimik, Claude Fable 5)
  render.video.format chooses mp4 or gif. Both come out of the same frame
  pipeline; a GIF gets a palette derived from its own frames in one ffmpeg pass,
  because the default fixed palette turns a gradient into bands.

  A GIF is an artifact kind of its own rather than a video with another
  extension, because the platforms treat it as one: Telegram wants sendAnimation
  (sendVideo accepts a GIF and then shows it as a still), X wants
  media_category=tweet_gif and a 15MB cap rather than 512MB, Reddit leases it as
  image/gif and submits it as an image. Instagram, Facebook, TikTok and LinkedIn
  take none, and a GIF aimed at one of them is refused by name before anything is
  rendered.

- add custom platforms backed by shell scripts ([f98560e](https://github.com/yohimik/crier/commit/f98560eea6eb52cec093fee3b581aa9d10f74cc9)) (by yohimik, Claude Fable 5)
  publish.custom.<name> defines a platform as a shell command. It is a peer of
  the nine built-ins rather than a lesser thing: it joins the fan-out, gets its
  own overlay and size, gets its caption templated, appears in crier ping and in
  a dry run, and its failure is a partial failure like any other.

  The contract is the environment in and a key=value file out — the command is
  told where the artifact is, what to say, and where it was staged, and answers
  with an id and a link. Exit 0 is the claim that it published.

  This is the one place the configuration's keys are not known in advance, so
  the names are discovered from all three layers before the environment binding
  is built — a platform can be introduced by the file, by CRIER_PUBLISH_CUSTOM_*,
  or by --set. The keys under a name stay closed: a typo in `commnad` is still
  refused by its full path.

- add self-update ([b2373d7](https://github.com/yohimik/crier/commit/b2373d743740cc19b13b75c5d0bc9aab3f1b230b)) (by yohimik, Claude Fable 5)
  `crier self-update` replaces the running binary with one from a crier release:
  the highest version rather than the most recent, verified against the sha256
  digest GitHub publishes and run once before anything is moved, swapped with two
  renames so a running executable can be replaced on Windows too. What it
  replaced is kept beside it for a week, which is what --rollback puts back, and
  the rotation is reversible so a second rollback returns.

  The design is dispat's, ported rather than imported and adapted to crier's own
  owner, repo, tag prefix and version line. The asset naming is now stated in one
  place as the contract it is: install.sh, install.ps1, `dispat install` and this
  all resolve the same six names.

  The GitHub token is only ever sent to github.com, so pointing --api-url at a
  mirror does not hand it credentials.

  Also: an end-to-end test that the same configuration written as YAML, JSON and
  TOML resolves identically. Formats are the loader's business rather than
  crier's, which is why nothing in crier would have noticed one breaking.

- add the ping command ([230bf01](https://github.com/yohimik/crier/commit/230bf0136112bcb269e8825ef52ff4f605a49052)) (by yohimik, Claude Fable 5)
  `crier ping` asks every enabled platform who the credentials belong to and
  posts nothing: one read-only identity endpoint each, plus a HEAD on the bucket
  when staging holds a credential. It is the way to find out a token is wrong
  that does not involve a real post on a real feed.

  LinkedIn is the awkward one — posting needs w_member_social and every endpoint
  that could name the account needs something else — so a 403 from userinfo is
  reported as a working token that cannot read a profile, and only a 401 fails.

  Publisher gains Ping, and the fan-out mirrors publish's: bounded, panic-safe,
  platform-ordered, with the same 0/4/5 exit codes.

- add the example gallery with bundled fonts ([d41c6c9](https://github.com/yohimik/crier/commit/d41c6c96434077e0da61956771f99631f53b11a9)) (by yohimik, Claude Fable 5)
  Five new examples across verticals plus two starters, each with its own
  typeface, palette and background treatment, and a preview rendered by the
  real binary. Fixes repeating gradients, which collapsed to a single band.

- add template pools, seeded randomisation and overflow goldens ([2960db3](https://github.com/yohimik/crier/commit/2960db39e9bf87ad0e2b220f0edf3a5787373249)) (by yohimik, Claude Fable 5)

- add the CLI, pipeline and render variants ([de064a3](https://github.com/yohimik/crier/commit/de064a3cae8f25b82beee80291395db8f5ebe525)) (by yohimik, Claude Fable 5)

- add nine platform publishers ([05079e8](https://github.com/yohimik/crier/commit/05079e8df72a762a5cbe26d7e76991c1a5698442)) (by yohimik, Claude Fable 5)
  Instagram, Facebook, TikTok, Telegram, X, Mastodon, Discord, LinkedIn
  and Reddit, with image and video flows, chunked uploads and a bounded
  fan-out that one platform's failure cannot stop.

### Fixes

- announce on the host the Instagram-login token parses on ([f5737d9](https://github.com/yohimik/crier/commit/f5737d975ee6b52fdcd89fa39ee4612a148197bd)) (by yohimik, Claude Fable 5)
  The announcement called graph.facebook.com with a token generated
  through Instagram business login, and Meta answered OAuthException 190,
  so v1.0.0-rc.1 went out unannounced. That token flavour lives on
  graph.instagram.com.

- drain the output before reaping, with a bound ([c0985dd](https://github.com/yohimik/crier/commit/c0985ddbf55728cab6c04db5a89fc33c3ee2b1da)) (by yohimik, Claude Fable 5)
  Reaping first lost output. cmd.Wait closes the pipes it owns as soon as the
  process exits, so bytes still sitting in the pipe buffer were discarded — the
  regression test for the ordinary case caught it three runs in five inside the
  container, which is exactly what it was written for.

  The readers now get a bounded head start and the process is reaped whatever
  they are doing. An ordinary process closes its own pipes when it exits, so the
  wait ends immediately and nothing is lost; a process that leaked its pipes to a
  grandchild costs one stop-timeout and is then reaped anyway, which is the hang
  this all began with.

- pick the right face from a font collection, and nine smaller raster fixes ([57ea82f](https://github.com/yohimik/crier/commit/57ea82fa35d8d966305fbc3d2c01a6f6f9800ae6)) (by yohimik, Claude Fable 5)
  The face index was parsed and then ignored: only face 0 of a .ttc was ever
  loaded, so a layout that chose face 3 had its glyph ids looked up in face 0's
  outlines — the wrong letters, drawn confidently, with no error anywhere. macOS
  and Windows ship most of their system fonts as collections, so this was most
  system text on both. Variable-font named instances were ignored for the same
  reason and rendered at their default weight.

  Gradient stops now interpolate premultiplied, as CSS requires. The headline
  case is unaffected in practice — webrender resolves `transparent` to the
  carried colour, so both maths agree — but two stops differing in both colour
  and alpha parted company badly: red to 20%-blue ran through a saturated purple
  instead of staying red until the blue had the alpha to show.

  Also: an oversized image is refused from its header rather than after decoding
  it (a 2KB PNG declaring 30000 square allocated 3.5GB first), and the pixel
  budget is no longer used as a byte cap that silently truncated any resource
  over 64MB; a group allocates its own box rather than a full page, which for a
  caption overlay on a story page is 1.2MB rather than 8.3MB, per group, per
  frame; a width without a height is refused instead of silently emitting no
  `@page` at all; the graphic stack pops on a defer so a panic cannot corrupt it; a
  bow-tie is no longer mistaken for a rectangular clip; the gradient quad is
  built on its own path; and an unrecognised blend mode warns instead of
  silently drawing normally.

- open request bodies late, and find a poster for a clip from disk ([d21bd16](https://github.com/yohimik/crier/commit/d21bd16837bf2885f0024e7b3fdd8a61ee253817)) (by yohimik, Claude Fable 5)
  A builder is not always sent. File opened the file as the builder was written
  and the streamed multipart body started its pipe and goroutine there too, so a
  request that never left — a bad URL, a later error — leaked a descriptor and a
  goroutine per attempt. Both are now acquired on the first read.

  `--publish-input clip.mp4` with Reddit enabled passed every precheck and then
  failed at the API for want of a poster image, because a clip crier was handed
  has no rendered frame 0. The frame is now pulled out of the file with ffmpeg;
  without ffmpeg the combination is refused upfront, naming Reddit and the file,
  rather than after the upload.

- report Instagram's real link, take GIFs in custom platforms, cap TikTok uploads ([bca27b0](https://github.com/yohimik/crier/commit/bca27b03a9df2626f535449ba0d7e294ceb9c923)) (by yohimik, Claude Fable 5)
  Three findings from the publish review.

  The Instagram link crier reported was instagram.com/p/<media-id>, and the media
  id is not the shortcode — so every successful post came with a 404. Only
  Instagram knows the shortcode, so it is now asked; a lookup that fails leaves
  the link empty rather than wrong, since the post exists either way.

  Custom.Needs dropped any kind word it did not recognise, "gif" included, so an
  enabled custom platform blocked every render.video.format=gif run with no way
  to opt in. gif is accepted, and an unrecognised word is a config error rather
  than a silent drop.

  TikTok had no size validation at all; a 4GB video was uploaded and then
  refused. The chunk arithmetic was checked against TikTok's media transfer
  guide and is correct as it stands — the final chunk may run to 128MB by design
  — and now has a test asserting every documented bound.

- keep credentials out of errors and logs, and bound uploads separately ([17e6445](https://github.com/yohimik/crier/commit/17e644532217b9ab119fbb84212f7ea85e87e2b7)) (by yohimik, Claude Fable 5)
  Two fixes in the shared HTTP client.

  url.Redacted only hides a password in the userinfo, which is the one place
  crier never puts a secret. The two places it does are a query parameter —
  Meta's access_token, an S3 signature — and Telegram's path, where the bot token
  is a path segment. Both reached APIError.Error(), which `crier publish` prints
  on every failure, and the retry warning, which logs the URL. Every URL crier
  renders now goes through a redactor that masks both.

  http.timeout bounded the body write as well as the response wait, so a 50MB
  video on a slow uplink failed deterministically at whatever size took longer
  than the timeout that makes ordinary API calls feel responsive — and NoRetry
  made it final. A request carrying over a megabyte, or one crier is streaming
  because it is too large to buffer, now gets http.upload-timeout instead.

- reap the process before draining its output ([734dd22](https://github.com/yohimik/crier/commit/734dd228b9e3b9d49bea78e05ba8dc14bd658c75)) (by yohimik, Claude Fable 5)
  Wait drained the output readers first, which hangs forever when the child
  leaves a background grandchild holding the write end of stdout — and because
  cmd.Wait was never reached, exec's WaitDelay never force-closed anything
  either. A custom platform whose script ends in `something &` hung crier past
  every timeout it has.

  The process is reaped first and the readers are then given a bounded window to
  drain what is already buffered, so ordinary output still arrives.

  Stop's escalation now kills the process group rather than the parent alone,
  matching what its polite step already did: killing only the parent leaves the
  children holding the port or the output file.

- scan fonts file by file so one bad font costs one font ([d1169b0](https://github.com/yohimik/crier/commit/d1169b0b13eed1aef813c1cc2d10f6be3e6586c1)) (by yohimik, Claude Fable 5)
  The system scan was a single call wrapped in a single recover, and the parser
  crashes rather than errors on a file it cannot make sense of. macOS ships one:
  /System/Library/Fonts/Supplemental/NISC18030.ttf is an Apple bitmap font whose
  summary loader returns before storing the summary it built, so reading its
  style dereferences a nil header. Every Mac therefore lost its entire font
  collection and rendered with the bundled Go faces, silently, on every run.

  Scanning one file at a time inside a per-file recover costs a deferred call per
  file and keeps the other 2,610 faces. Skipped files are counted, summarised in
  one warning and named at debug. The whole-scan recover stays as a last-resort
  net for a crash outside any single file.

  crier now writes the font cache itself, since the library's own ScanAndCache
  does the very whole-directory scan this avoids. The survivors are cached even
  though a Mac always skips a file or two — rescanning hundreds of fonts per
  render would be a poor trade — but a scan that lost more than a quarter of the
  collection is not cached, because that is the shape of a transient failure and
  a cache is the wrong place to make one permanent.

  fontconfig's bare log.Println lines now go through zerolog as debug records
  rather than landing raw on standard error.

- check a clip against its own kind, not against video ([cd8fa8a](https://github.com/yohimik/crier/commit/cd8fa8a15af64219756abd8eb52ca0c10c2fe999)) (by yohimik, Claude Fable 5)
  Artifacts.Primary asked whether a platform accepts video whatever the clip
  actually was. A GIF lives in the same field, so one aimed at Instagram —
  which takes video and not animations — passed the check and would have been
  uploaded as if it were an MP4. It now asks about the artifact's own kind, and
  publish.input gets the same early refusal a rendered clip already had.

- render on a machine with no fonts installed ([ebd7e47](https://github.com/yohimik/crier/commit/ebd7e476518172d538903ad5eb28d67f19102ef7)) (by yohimik, Claude Fable 5)
  A scratch container and most CI images have no fonts at all, and the system
  scan reported "no font directory found" — which crier turned into a refusal to
  render. It ships its own faces precisely so it can render there, and a static
  binary that needs fonts installed alongside it is not the thing crier claims to
  be. The scan is now best-effort: a failure is a warning and the bundled faces,
  the same as a crash already was.

  Found by the containerised test gate, which is the only place with no fonts.

### Authors

- yohimik
- Claude Fable 5


This file is written by [dispat](https://dispat.dev) from the conventional
commits, one section per release. Nothing has been released yet — see
[docs/operations/release.md](../../docs/operations/release.md) for how the first
one is cut.
