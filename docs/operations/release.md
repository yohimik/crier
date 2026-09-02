# Releasing

Releases are cut with [dispat](https://dispat.dev). It reads the conventional commits since the last tag and works out the version. Next, it writes the changelog. Finally, it tags, builds, and publishes a GitHub release with the six binaries attached.

```sh
dispat status     # what would be released, and why
```

You can find the whole configuration in [`dispat.yaml`](../../dispat.yaml).

## Running one

The **release** workflow uses `workflow_dispatch`. It has four jobs:

1. **plan**: Runs `dispat status --require-release`. Exit 3 means the commits hold nothing releasable. The run stops there.
2. **ping**: Runs `crier ping` with the previous release's binary against the announcement's credentials. Nothing is posted. A revoked or expired token fails the run here, in a minute, instead of surfacing as a skipped announcement at the end. A repository without the announcement secrets skips this and releases without announcing.
3. **tests**: Runs every gate against the commit about to be tagged.
4. **release**: Runs `dispat --log-format json`. This one command handles the version, changelog, commit, tag, build and GitHub release.
5. **install**: Runs on Ubuntu, macOS and Windows. It installs the published binary and runs it. A release is not finished until its assets install.

It needs no secrets beyond the automatic `GITHUB_TOKEN`. The announcement secrets are optional; when they are set, the ping job holds the release to them.

## Commits

Make your commits conventional, and **scoped to the package**:

```
feat(crier): add the reddit publisher
fix(crier): stop transforming the glyph cache in place
feat(crier)!: rename the stage modes
```

The scope matters. Without it, dispat attributes a commit by its changed files. Only files under `cmd/crier`, the package's path, would count. A change under `internal/` would be attributed to nothing and released as nothing.

## The rc train

crier releases on two channels: `rc` and `stable`. dispat needs no declaration to *use* a channel. It reads one out of the baseline tag. This means `dispat.yaml` only declares which names are allowed:

```yaml
parser:
  allowedChannels: [rc]
```

This turns `%re` or `%RC` into a named error (E181). It prevents the quiet start of a second train that nobody notices until two versions are in flight. `stable` is always accepted.

| | rc | stable |
| --- | --- | --- |
| Tag | `v1.0.0-rc.0` | `v1.0.0` |
| GitHub release | created, **flagged prerelease** | created |
| Changelog entry | written | written |
| Alias tags moved | none | none |


Both channels get a changelog entry and a GitHub release. This is the default for dispat. It is also what crier wants. If nobody can read the notes for a release candidate, nobody tries it. dispat flags the GitHub release as a prerelease whenever the version is one. It does this without being asked. This keeps the install scripts and `dispat install` from resolving to it.

crier publishes **no alias tags**. A moving ref like `v1` would have to say `channels: [stable]` so a candidate could not drag it forward. There is none to drag.

## The first release

The `initials` block is deliberately left out of `dispat.yaml`. This means the baseline is `0.0.0`. A **breaking change on the rc channel** cuts the first release:

```
feat(crier)%rc!: the first release

BREAKING CHANGE: the first published version.
```

- `%rc` puts the package on the `rc` channel. The published version becomes `1.0.0-rc.0` instead of `1.0.0`.
- `!` raises the bump to major. This takes `0.0.0` to `1.0.0`.

**The order matters.** The header grammar is `<type>[(<scope>)][<directives>][!]: <description>`. The channel directive must come before the `!`. Writing `feat(crier)!%rc:` causes a parse error (E120, "expected ':' but found '%'"). A message dispat cannot parse contributes nothing. As a result, the release would come out on `stable`.

Using the footer form avoids this problem entirely:

```
feat(crier): the first release

BREAKING CHANGE: the first published version.
Channel: rc
```

**Check before you push.** Run `dispat status` to print the plan without touching anything. You need to read the version line:

```
$ dispat status
● changed  package=crier channel="stable -> rc" version="0.0.0 -> 1.0.0-rc.0"
```

If it says `0.0.0 -> 1.0.0` with `channel=stable`, the directive failed. Fix the commit and check again.

### Riding the train

Once the package is on `rc`, ordinary commits keep it there. The dispat tool reads the channel from the baseline tag. You do not need a directive:

```
fix(crier): a fix          ->  1.0.0-rc.0 -> 1.0.0-rc.1
feat(crier): a feature     ->  1.0.0-rc.1 -> 1.0.0-rc.2
```

Each candidate gets its own changelog entry and GitHub release. These document only its specific changes. For example, `rc.1` does not repeat the changes from `rc.0`.

### Promoting to stable

A transition graduates the package:

```
release(crier)%rc>stable: promote the first release
```

```
● changed  package=crier channel="rc -> stable" version="1.0.0-rc.1 -> 1.0.0"
```

The `<from>><to>` form matches against the *baseline* channel. This makes it idempotent. A package that already graduated does not match. This means the same directive is safe to write again. The `release(crier)%stable:` format also graduates the package, but it lacks this property.

Graduation collects the whole train into one changelog entry for stable readers. The version is computed over the entire train, not just the last candidate.

### One caveat about commit errors

`commitErrors` stays at its default setting of `warn`. A commit dispat cannot parse contributes nothing, but the run continues. The seven commits that predate crier's conventional-commit convention are unparseable. Setting `commitErrors: error` would refuse every release until the first tag exists. After that tag exists, those commits fall outside the window. Then the stricter setting becomes available. You should turn it on once `v1.0.0-rc.0` is tagged.

### What a release candidate means downstream

It is a prerelease. Downstream tools treat it as one:

- **`install.sh` and `install.ps1` skip it.** They resolve the highest *stable* version. A version with a hyphen in it is not stable. During the rc period, you must name it: `CRIER_VERSION=1.0.0-rc.0 sh install.sh`.
- **`dispat install` needs `--prerelease`**:

  ```sh
  dispat install yohimik/crier --prerelease
  ```

- **`crier self-update` needs it too**. This is for the same reason:

  ```sh
  crier self-update --prerelease
  ```

- **`go install …@latest` skips it**. This follows Go's own prerelease rule. Running `go install …@v1.0.0-rc.0` works.

### The anthems

The release posts a third story: the cover page, held for sixteen seconds as a
video with a fanfare under it. Video is the one way audio reaches Instagram,
which takes no audio file and no track id. The pass needs ffmpeg on the runner
and skips with a log line where there is none.

There are four fanfares, listed under `render.video.audio-pool` in
`announce/crier.yaml`: the 1812 Overture finale, the last strain of The Stars
and Stripes Forever, the William Tell galop, and the end of In the Hall of the
Mountain King. Each is sixteen seconds of a public-domain recording,
normalised to the same loudness so one release is not twice as loud as the
last. `announce/anthem.md` carries the provenance of all four.

The release does not choose between them and neither does anybody else. The
same version-derived seed that picks the layout picks the anthem, so a release
has a soundtrack of its own, re-running it plays the same one, and the next
release is something else. The pick is logged:

```
INF picked an audio track from the pool audio=announce/anthem-william-tell.mp3 pool=4
```

## The assets

Six bare, uncompressed binaries:

```
crier-linux-amd64    crier-darwin-amd64    crier-windows-amd64.exe
crier-linux-arm64    crier-darwin-arm64    crier-windows-arm64.exe
```

The names are a contract. `install.sh`, `install.ps1` and a bare `dispat install` look for the repository's name and the platform. They all resolve exactly these. There are no archives and no checksum file. GitHub publishes a sha256 digest per asset, and all three verify against it.

They are built by the root `Dockerfile`'s `export` target. This target descends from the `test` target. What lands in `dist/` is six validated binaries. They are executed where possible and put through both suites. Six binaries that merely compiled are never uploaded.

### The binaries are smoke-tested before upload

Three checks stand between a compile and an upload. They are deliberately different in kind:

1. **Every binary is read back.** `go version -m` has to report the `GOOS` and `GOARCH` the file name claims. A build loop cannot silently produce six copies of one platform and get past this.
2. **The native one is run.** `crier --version` has to report `linux/<arch>` and the version being released. This checks that the ldflags stamping took.
3. **The native one is put through the end-to-end smoke subset.** The suite runs against the artefact itself. `CRIER_E2E_BINARY` points at `crier-linux-<arch>` and nothing is rebuilt. This exercises the exact bytes that will be uploaded: a render, the configuration precedence across file, environment and flags, the thirteen-platform image fan-out against fake servers, and the version stamp.

The subset is every test named `TestSmoke...` in `test/e2e`, run as `-run '^TestSmoke'`. It is small on purpose. The full suite already ran in the `test` stage against an instrumented build. This pass exists to catch what only the released artefact can be wrong about. This includes a bad link, a missing embedded asset, or a stamp that did not apply.

Running it by hand against any binary:

```sh
CRIER_E2E_BINARY=/path/to/crier go test -tags e2e ./test/e2e -run '^TestSmoke' -v
```

darwin and windows cannot run in the build at all. Proving those falls to the release's post-release install matrix. This installs the published asset on each runner and runs `crier --version`.

## The release announces itself

Every release posts a feed card and a story to Instagram, and the release to LinkedIn: the anthem clip carries the announcement, and the changelog pages follow as one multi-image album. A LinkedIn post takes one video or many images, never both, which is why it is two posts. The video API is a partner product (LinkedIn's Community Management API) that a plain member token does not carry. When LinkedIn refuses the clip, the pass falls back to the whole card as one album, cover first, so the release reaches LinkedIn with whatever the token allows. The commentary is LinkedIn's own, written around the fact that the post is generated: the changelog comes from the commits, and the render and the publish both happen inside the GitHub Actions run. LinkedIn takes the bytes directly, so that pass needs no tunnel. The `crier` tool renders these from the binary that the release just built. It runs in the `announce` stage of `dispat`. This stage only ever warns. The `announce/announce.sh` script exits with 0 for any reason it decides not to post. **A missing secret can never turn a good release red.**

The card is located in [`announce/`](../../announce/). This folder contains two templates, a config, and a script. The script turns the release-notes variables into data for the card.

### No two releases look alike

There are two layouts, [`template.html`](../../announce/template.html) and [`template-b.html`](../../announce/template-b.html), and each of them draws its accent colours out of a small palette. crier picks one layout and one palette per run, from its [seeded random source](../templates/pools.md). So the release you are reading about had a look of its own, and the next one will have a different one.

The two layouts are different faces on one card, not two cards. Everything that carries meaning is shared between them letter for letter: the page box, the running header and footer, the string that feeds the small version badge, the cover height that pins the install block, and the wrapping rules that decide what a long release subject does. Only the look differs. That is what lets the announce tests measure the same pixels whichever one a release drew.

The seed is the version. `announce/announce.sh` derives it once, near the top, as the `cksum` of the version string, and passes it to every crier run it makes:

```sh
seed=$(printf '%s' "$version" | cksum | cut -d' ' -f1)
```

This matters because the announcement is not one crier run. It is the anthem render, the feed post, the story pass and the LinkedIn passes, and each one would otherwise draw a seed of its own. The stories would come out as a different-looking release than the feed post they follow.

The seed is logged, so the run says what it chose:

```
announce: seed for v1.1.0 is 487772968
```

To draw a given release's card again on your own machine, hand that number back:

```sh
DISPAT_NEW_VERSION=1.1.0 sh announce/notes.sh |
  crier render --config announce/crier.yaml --render-data - \
    --render-seed "$(printf '%s' 1.1.0 | cksum | cut -d' ' -f1)" \
    --render-format png --render-output announce/preview.png
```

That is also how the committed previews are made: version 1.1.0, seed 487772968. Pinning it is what keeps them from changing every time somebody regenerates them.

The card paginates. Page one is the cover: the version badge and the three install routes, each pinned to the version. The changelog carries on across the pages after it, under a small version badge and a footer that numbers the page. A committed preview shows both: [page one](../../announce/preview-1.png) and [page two](../../announce/preview-2.png).

Each pass turns those pages into what its surface takes. The feed post becomes one carousel. The story pass posts one story per page, in order, each one live before the next is created. Neither is configured. crier works it out from the platform.

A section shows up to twenty entries and says `+N more` past that. The ceiling is there so a release cannot push the render past `render.pages-max`, which refuses it rather than truncating.

### Secrets

| Secret | What it is | Without it |
| ------ | ---------- | ---------- |
| `CRIER_PUBLISH_INSTAGRAM_TOKEN` | a long-lived Instagram Graph token; see [Instagram](../publishing/instagram.md) | nothing is posted |
| `CRIER_PUBLISH_INSTAGRAM_USER_ID` | the Instagram business account id | nothing is posted |
| `NGROK_AUTHTOKEN` | an ngrok authtoken, for the tunnel | nothing is posted |
| `CRIER_PUBLISH_LINKEDIN_TOKEN` | a LinkedIn OAuth token with `w_member_social`; see [LinkedIn](../publishing/linkedin.md) | the LinkedIn post is skipped |
| `LINKEDIN_URN` | the author URN, e.g. `urn:li:person:XXXX` | the LinkedIn post is skipped |

The first two secrets are set. **`NGROK_AUTHTOKEN` still has to be added**. Until you add it, every release logs what is missing and skips the announcement.

### Why a tunnel

Instagram does not accept raw bytes. It requires a public URL to fetch media from its own servers. A GitHub runner has no public address. To solve this, `crier` serves the file itself. Then, `ngrok` gives that server a public URL for the length of the run. The workflow only installs `ngrok` when the secret exists. This means a repository without the secret does not spend a minute on `apt` for nothing.

> **If the containers come back `ERROR`, suspect the tunnel first.** The free tier of `ngrok` serves an interstitial page to first-time visitors. Meta's fetcher receives that page instead of the image. This is the first thing to check. It is explained alongside alternative options in [tunnels](../staging/tunnels.md).

### Staging somewhere else

The `announce.sh` script honors the `CRIER_STAGE_*` environment variables. You can use a bucket to replace the tunnel without editing anything:

```sh
CRIER_STAGE_MODE=s3
CRIER_STAGE_S3_ENDPOINT=…
CRIER_STAGE_S3_BUCKET=…
CRIER_STAGE_S3_ACCESS_KEY=…
CRIER_STAGE_S3_SECRET_KEY=…
```

If you set `CRIER_STAGE_MODE` to anything other than `server`, the script does not want `NGROK_AUTHTOKEN`. Its absence is no longer a reason to skip the announcement.

### Turning it off

You can remove the Instagram secrets. Alternatively, you can drop `announce.sh` from the `announce` script in `dispat.yaml`. The two lines above it write the workflow's outputs. Later jobs need those outputs.

### Running it again for a released version

The script reads the release out of its environment. You can replay it for any version from a checkout with the binaries built:

```sh
sh cmd/crier/build.sh                     # or download the release's assets
DISPAT_NEW_VERSION=1.2.3 \
DISPAT_FEATURES="add streaming
add retries" \
DISPAT_FIXES="close a leak" \
CRIER_PUBLISH_INSTAGRAM_TOKEN=… \
CRIER_PUBLISH_INSTAGRAM_USER_ID=… \
NGROK_AUTHTOKEN=… \
  sh announce/announce.sh
```

To see the card without posting anything, run this:

```sh
DISPAT_NEW_VERSION=1.2.3 sh announce/notes.sh |
  crier render --config announce/crier.yaml --render-data - --render-output card.jpg
```

The config renders a JPEG because that is what Instagram fetches. Add `--render-format png` for a lossless copy. With no `--render-seed` this draws a fresh layout and palette every time, which is a quick way to see what the pool holds; add the seed to pin one, as above.

## Versioned documentation

On release, copy the tree for the tag:

```sh
mkdir -p docs/versions/v1.0.0 && cp -r docs/* docs/versions/v1.0.0/
```

This way, a reader on an older binary can find the pages that described it.
