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

## Prerequisites

None for images. **ffmpeg** for [video](../rendering/video.md), and **ngrok**
or **zrok** for a [tunnel](../staging/tunnels.md) — both only when those
features are used, and both checked before crier does any work.

## Verifying

```sh
crier version
crier platforms      # what is configured, and what is missing
```
