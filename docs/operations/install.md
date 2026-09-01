# Installing

Every option produces the same static binary. No runtime, nothing to link
against.

## The install script

```sh
curl -fsSL https://raw.githubusercontent.com/yohimik/crier/main/install.sh | sh
```

```powershell
irm https://raw.githubusercontent.com/yohimik/crier/main/install.ps1 | iex
```

It resolves the latest stable release, verifies the asset against the sha256
digest GitHub publishes for it, and installs to `/usr/local/bin` when that is
writable or `~/.local/bin` otherwise (`%LOCALAPPDATA%\crier\bin` on Windows).

The resolved version goes to standard output alone, and everything else to
standard error, so a caller can capture it:

```sh
version=$(curl -fsSL …/install.sh | sh)
```

Options, as flags or as environment variables:

| Flag | Variable | Meaning |
| ---- | -------- | ------- |
| `--version 1.2.3` | `CRIER_VERSION` | a specific release |
| `--bin-dir DIR` | `CRIER_BIN_DIR` | where to install |
| `--os`, `--arch` | | cross-install, for a container image |
| `--token` | `GITHUB_TOKEN` | raises the API rate limit |

## dispat

```sh
dispat install yohimik/crier --asset 'crier-{os}-{arch}'
```

The `--asset` pattern is **required**: a crier release carries six binaries, and
a bare `dispat install` only resolves when a release has exactly one.

Before the first stable release, add `--prerelease` — the release candidates
are prereleases and are skipped by default.

## go install

```sh
go install github.com/yohimik/crier/cmd/crier@latest
```

Needs Go 1.26 or newer. The binary reports the module version rather than the
one stamped at release time, which is the only difference.

## From source

```sh
git clone https://github.com/yohimik/crier
cd crier
go build -o crier ./cmd/crier
```

See [development](./development.md).

## Keeping it up to date

```sh
crier self-update            # replace this binary with the newest release
crier self-update --check    # is there a newer one? exit 1 if so, and change nothing
```

The download is verified against the sha256 digest GitHub publishes for the
asset — the same digest the install script checks — and run once before
anything is moved. Nothing is replaced until every check has passed, so a
failed update leaves the working binary exactly where it was.

The binary it replaced is kept beside it as `crier.backup` (`crier.backup.exe`
on Windows) for a week, and every later run prunes it once it is older than
that. Within the week:

```sh
crier self-update --rollback
```

The rollback rotates rather than moves, so it is itself reversible: running it
twice returns to where you started.

| Flag | What it does |
| ---- | ------------ |
| `--check` | report whether a newer release exists; exit 1 when not current |
| `--release <v>` | install that version rather than the newest |
| `--prerelease` | consider release candidates too |
| `--rollback` | put the binary the last update replaced back |
| `--api-url` | a GitHub Enterprise API, or a mirror |
| `--token-env` | the variable holding a GitHub token (default `GITHUB_TOKEN`) |
| `--json` | the report as JSON |

The token is only ever sent to github.com. Point `--api-url` somewhere else and
the token stays behind, because a mirror somebody is testing against has no
business receiving GitHub credentials.

On Windows the replacement is two renames rather than a write, because Windows
refuses to overwrite a running executable but will let it be renamed out of the
way. The same dance runs everywhere, so there is one code path rather than two.

### A `go install` build updates differently

`self-update` refuses to replace a binary the Go toolchain owns, and says so:

```sh
go install github.com/yohimik/crier/cmd/crier@latest
```

## The asset names are a contract

A release carries six bare binaries:

```
crier-linux-amd64    crier-darwin-amd64    crier-windows-amd64.exe
crier-linux-arm64    crier-darwin-arm64    crier-windows-arm64.exe
```

Four separate things resolve those names, and renaming an asset breaks all four
at once:

- `install.sh` and `install.ps1`, which build the name from the detected
  platform;
- `dispat install yohimik/crier --asset 'crier-{os}-{arch}'`;
- `crier self-update`, whose `AssetName` mirrors the same rule;
- the `Dockerfile`'s cross-compile loop, which is what produces them.

There is no archive and no checksum file: GitHub publishes a sha256 digest per
asset, and every route above verifies against it.

## Prerequisites

None for images. **ffmpeg** for [video](../rendering/video.md), and **ngrok**
or **zrok** for a [tunnel](../staging/tunnels.md) — both only when those
features are used, and both checked before crier does any work.

## Verifying

```sh
crier --version
crier platforms      # what is configured, and what is missing
```
