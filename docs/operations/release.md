# Releasing

Releases are cut by [dispat](https://dispat.dev): it reads the conventional
commits since the last tag, works out the version, writes the changelog, tags,
builds, and publishes a GitHub release with the six binaries attached.

```sh
dispat status     # what would be released, and why
```

The whole configuration is [`dispat.yaml`](../../dispat.yaml).

## Running one

The **release** workflow, `workflow_dispatch`. It has four jobs:

1. **plan** — `dispat status --require-release`. Exit 3 means the commits hold
   nothing releasable, and the run stops there.
2. **tests** — every gate, against the commit about to be tagged.
3. **release** — `dispat --log-format json`, one command for version,
   changelog, commit, tag, build and GitHub release.
4. **install** — on Ubuntu, macOS and Windows: install the published binary
   and run it. A release is not finished until its assets install.

No secrets beyond the automatic `GITHUB_TOKEN`.

## Commits

Conventional, and **scoped to the package**:

```
feat(crier): add the reddit publisher
fix(crier): stop transforming the glyph cache in place
feat(crier)!: rename the stage modes
```

The scope matters. Without it dispat attributes a commit by its changed files,
and only files under `cmd/crier` — the package's path — would count; a change
under `internal/` would be attributed to nothing and released as nothing.

## The rc train

crier releases on two channels: `rc` and `stable`. dispat needs no declaration
to *use* a channel — it reads one out of the baseline tag — so `dispat.yaml`
declares only which names are allowed:

```yaml
parser:
  allowedChannels: [rc]
```

That turns `%re` or `%RC` into a named error (E181) rather than the quiet start
of a second train nobody notices until two versions are in flight. `stable` is
always accepted.

| | rc | stable |
| --- | --- | --- |
| Tag | `v1.0.0-rc.0` | `v1.0.0` |
| GitHub release | created, **flagged prerelease** | created |
| Changelog entry | written | written |
| Alias tags moved | — | — |

Both channels get a changelog entry and a GitHub release: that is dispat's
default and it is what crier wants, because a release candidate nobody can read
the notes for is a release candidate nobody tries. dispat flags the GitHub
release as a prerelease whenever the version is one, without being asked, which
is what keeps the install scripts and `dispat install` from resolving to it.

crier publishes **no alias tags**. A moving ref like `v1` would have to say
`channels: [stable]` so a candidate could not drag it forward; there is none to
drag.

## The first release

There is deliberately no `initials` block in `dispat.yaml`, so the baseline is
`0.0.0`. The first release is cut by a **breaking change on the rc channel**:

```
feat(crier)%rc!: the first release

BREAKING CHANGE: the first published version.
```

- `%rc` puts the package on the `rc` channel, so the version published is
  `1.0.0-rc.0` rather than `1.0.0`.
- `!` raises the bump to major, taking `0.0.0` to `1.0.0`.

**The order matters.** The header grammar is
`<type>[(<scope>)][<directives>][!]: <description>`, so the channel directive
comes before the `!`. Writing `feat(crier)!%rc:` is a parse error (E120,
"expected ':' but found '%'"), and a message dispat cannot parse contributes
nothing — the release would come out on `stable`.

The footer form avoids the question entirely:

```
feat(crier): the first release

BREAKING CHANGE: the first published version.
Channel: rc
```

**Check before you push.** `dispat status` prints the plan without touching
anything, and the line to read is the version:

```
$ dispat status
● changed  package=crier channel="stable -> rc" version="0.0.0 -> 1.0.0-rc.0"
```

If that says `0.0.0 -> 1.0.0` with `channel=stable`, the directive did not
take. Fix the commit and look again.

### Riding the train

Once the package is on `rc`, ordinary commits keep it there. dispat reads the
channel from the baseline tag, so no directive is needed:

```
fix(crier): a fix          ->  1.0.0-rc.0 -> 1.0.0-rc.1
feat(crier): a feature     ->  1.0.0-rc.1 -> 1.0.0-rc.2
```

Each candidate's changelog entry and GitHub release document only its own
changes, so `rc.1` does not repeat `rc.0`.

### Promoting to stable

A transition graduates the package:

```
release(crier)%rc>stable: promote the first release
```

```
● changed  package=crier channel="rc -> stable" version="1.0.0-rc.1 -> 1.0.0"
```

The `<from>><to>` form matches against the *baseline* channel, which makes it
idempotent: a package that already graduated does not match, so the same
directive is safe to write again. `release(crier)%stable:` graduates too, but
without that property.

The graduation collects the whole train into the one changelog entry stable
readers see, and the version is computed over the train rather than over the
last candidate alone.

### One caveat about commit errors

`commitErrors` is left at its default, `warn`: a commit dispat cannot parse
contributes nothing and the run continues. The seven commits that predate
crier's conventional-commit convention are unparseable, and setting
`commitErrors: error` would refuse every release until the first tag exists —
after which they fall outside the window and the stricter setting becomes
available. Turning it on is worth doing once `v1.0.0-rc.0` is tagged.

### What a release candidate means downstream

A prerelease, and the tools treat it as one:

- **`install.sh` and `install.ps1` skip it.** They resolve the highest *stable*
  version, and a version with a hyphen in it is not stable. During the rc
  period, name it: `CRIER_VERSION=1.0.0-rc.0 sh install.sh`.
- **`dispat install` needs `--prerelease`**:

  ```sh
  dispat install yohimik/crier --asset 'crier-{os}-{arch}' --prerelease
  ```

- **`crier self-update` needs it too**, for the same reason:

  ```sh
  crier self-update --prerelease
  ```

- **`go install …@latest` skips it**, which is Go's own prerelease rule.
  `go install …@v1.0.0-rc.0` works.

## The assets

Six bare, uncompressed binaries:

```
crier-linux-amd64    crier-darwin-amd64    crier-windows-amd64.exe
crier-linux-arm64    crier-darwin-arm64    crier-windows-arm64.exe
```

The names are a contract: `install.sh`, `install.ps1` and `dispat install
--asset 'crier-{os}-{arch}'` all resolve exactly these. No archives and no
checksum file — GitHub publishes a sha256 digest per asset, and all three
verify against it.

They are built by the root `Dockerfile`'s `export` target, which descends from
the `test` target: what lands in `dist/` is six binaries that were validated,
executed where they could be, and put through both suites — never six that
merely compiled.

### The binaries are smoke-tested before upload

Three checks stand between a compile and an upload, and they are deliberately
different in kind:

1. **Every binary is read back.** `go version -m` has to report the `GOOS` and
   `GOARCH` the file name claims, so a build loop that silently produced six
   copies of one platform cannot get past this.
2. **The native one is run.** `crier --version` has to report `linux/<arch>`
   and the version being released, which is the check that the ldflags
   stamping took.
3. **The native one is put through the end-to-end smoke subset.** The suite
   runs against the artefact itself — `CRIER_E2E_BINARY` points at
   `crier-linux-<arch>` and nothing is rebuilt — so what is exercised is the
   exact bytes that will be uploaded: a render, the configuration precedence
   across file, environment and flags, the nine-platform fan-out against fake
   servers, and the version stamp.

The subset is every test named `TestSmoke...` in `test/e2e`, run as
`-run '^TestSmoke'`. It is small on purpose: the full suite already ran in the
`test` stage against an instrumented build, and this pass exists to catch what
only the released artefact can be wrong about — a bad link, a missing embedded
asset, a stamp that did not apply.

Running it by hand against any binary:

```sh
CRIER_E2E_BINARY=/path/to/crier go test -tags e2e ./test/e2e -run '^TestSmoke' -v
```

darwin and windows cannot run in the build at all. Proving those falls to the
release's post-release install matrix, which installs the published asset on
each runner and runs `crier --version`.

## The release announces itself

Every release posts to Instagram — a feed card and a story — rendered by crier,
from the binary that release just built. It runs in dispat's `announce` stage,
which only ever warns, and `announce/announce.sh` exits 0 for every reason not
to post. **A missing secret can never turn a good release red.**

The card is [`announce/`](../../announce/): a template, a config, a script that
turns the release-notes variables into its data, and a committed
[preview](../../announce/preview.png). It carries the version, the first three
entries of each notes section with a `+N more` when there are more, and all
three install routes pinned to the version.

### Secrets

| Secret | What it is | Without it |
| ------ | ---------- | ---------- |
| `CRIER_PUBLISH_INSTAGRAM_TOKEN` | a long-lived Instagram Graph token — see [Instagram](../publishing/instagram.md) | nothing is posted |
| `CRIER_PUBLISH_INSTAGRAM_USER_ID` | the Instagram business account id | nothing is posted |
| `NGROK_AUTHTOKEN` | an ngrok authtoken, for the tunnel | nothing is posted |

The first two are set. **`NGROK_AUTHTOKEN` still has to be added** — until it
is, every release logs what is missing and skips the announcement.

### Why a tunnel

Instagram does not accept bytes. It takes a public URL and fetches the media
from its own servers, and a GitHub runner has no public address — so crier
serves the file itself and ngrok gives that server a URL for the length of the
run. The workflow installs ngrok only when the secret exists, so a repository
without one does not spend a minute on apt for nothing.

> **If the containers come back `ERROR`, suspect the tunnel first.** ngrok's
> free tier serves an interstitial page to first-time visitors, and Meta's
> fetcher receives that page instead of the image. It is the first thing to
> check, and it is explained with the alternatives in
> [tunnels](../staging/tunnels.md).

### Staging somewhere else

`announce.sh` honours the `CRIER_STAGE_*` environment, so a bucket replaces the
tunnel without editing anything:

```sh
CRIER_STAGE_MODE=s3
CRIER_STAGE_S3_ENDPOINT=…
CRIER_STAGE_S3_BUCKET=…
CRIER_STAGE_S3_ACCESS_KEY=…
CRIER_STAGE_S3_SECRET_KEY=…
```

With `CRIER_STAGE_MODE` set to anything but `server`, `NGROK_AUTHTOKEN` is not
wanted and its absence is not a reason to skip.

### Turning it off

Remove the Instagram secrets, or drop `announce.sh` from the `announce` script
in `dispat.yaml` — the two lines above it write the workflow's outputs and are
what the later jobs need.

### Running it again for a released version

The script reads the release out of its environment, so it can be replayed for
any version from a checkout with the binaries built:

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

To see the card without posting anything:

```sh
DISPAT_NEW_VERSION=1.2.3 sh announce/notes.sh |
  crier render --config announce/crier.yaml --render-data - --render-output card.jpg
```

The config renders JPEG, because that is what Instagram fetches. Add
`--render-format png` for a lossless copy.

## Versioned documentation

On release, copy the tree for the tag:

```sh
mkdir -p docs/versions/v1.0.0 && cp -r docs/* docs/versions/v1.0.0/
```

so a reader on an older binary can find the pages that described it.
