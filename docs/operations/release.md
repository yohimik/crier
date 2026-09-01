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

## The first release

There is deliberately no `initials` block in `dispat.yaml`, so the baseline is
`0.0.0`. The first release is cut by a **breaking change on the rc channel**:

```
feat(crier)!%rc: the first release
```

- `!` raises the bump to major, taking `0.0.0` to `1.0.0`.
- `%rc` puts the package on the `rc` channel, so the version published is
  `1.0.0-rc.0` rather than `1.0.0`.

The footer form is equivalent and unambiguous:

```
feat(crier): the first release

BREAKING CHANGE: the first release
Channel: rc
```

Subsequent commits stay on the train — `1.0.0-rc.1`, `1.0.0-rc.2` — with no
directive, because dispat reads the channel from the baseline tag. Graduating
is a directive of its own:

```
release(crier)%stable: graduate
```

which publishes `1.0.0`.

### What a release candidate means downstream

A prerelease, and the tools treat it as one:

- **`install.sh` and `install.ps1` skip it.** They resolve the highest *stable*
  version, and a version with a hyphen in it is not stable. During the rc
  period, name it: `install.sh --version 1.0.0-rc.0`.
- **`dispat install` needs `--prerelease`**:

  ```sh
  dispat install yohimik/crier --asset 'crier-{os}-{arch}' --prerelease
  ```

- **The changelog and the GitHub release are written as usual.** The rc channel
  changes the version, not whether a release is published.
- **`go install …@latest` skips it too**, which is Go's own prerelease rule.
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

## Versioned documentation

On release, copy the tree for the tag:

```sh
mkdir -p docs/versions/v1.0.0 && cp -r docs/* docs/versions/v1.0.0/
```

so a reader on an older binary can find the pages that described it.
