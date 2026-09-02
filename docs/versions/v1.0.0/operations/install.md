# Installing

Every option produces the same static binary. It needs no runtime. There is nothing to link against.

## The install script

```sh
curl -fsSL https://raw.githubusercontent.com/yohimik/crier/main/install.sh | sh
```

```powershell
irm https://raw.githubusercontent.com/yohimik/crier/main/install.ps1 | iex
```

The script resolves the latest stable release. It verifies the asset against the sha256 digest GitHub publishes for it. It installs to `/usr/local/bin` when that is writable. Otherwise, it installs to `~/.local/bin`. On Windows, it uses `%LOCALAPPDATA%\crier\bin`.

The resolved version goes to standard output alone. Everything else goes to standard error. This lets a caller capture it:

```sh
version=$(curl -fsSL …/install.sh | sh)
```

You can pass options as flags or as environment variables:

| Flag | Variable | Meaning |
| ---- | -------- | ------- |
| `--version 1.2.3` | `CRIER_VERSION` | a specific release |
| `--bin-dir DIR` | `CRIER_BIN_DIR` | where to install |
| `--os`, `--arch` | | cross-install, for a container image |
| `--token` | `GITHUB_TOKEN` | raises the API rate limit |


## dispat

```sh
dispat install yohimik/crier
```

crier publishes its binaries under the name the dispat installer expects when no `--asset` is given. This name is the repository's name and the platform, with `.exe` on Windows. As a result, a bare invocation resolves on its own with dispat 1.7 or newer. An older dispat needs the pattern spelled out: `--asset 'crier-{os}-{arch}'`. With older versions, a bare `dispat install` only resolves when a release has exactly one.

Release candidates are prereleases and are skipped by default; `--prerelease` opts in to them.

## GitHub Actions

```yaml
- uses: yohimik/crier@v1
- run: crier
```

This is a composite action. It runs the same install script described on this page and adds the install directory to `PATH`. It is checked out with the repository it lives in. This means the installer it runs is the one that shipped in the ref you pinned. The action cannot drift from the script it wraps, and nothing is downloaded to bootstrap it.

### Inputs

| Input | Default | What it does |
| ----- | ------- | ------------ |
| `version` | the latest stable | The version or tag to install: `1.2.3` or `v1.2.3`. |
| `bin-dir` | `$HOME/.crier/bin` | Where to install. Under `HOME`, so it needs no sudo and behaves the same on all three runner operating systems. |
| `github-token` | `${{ github.token }}` | Token for the releases API. |

The token only raises the rate limit. The releases for crier are public, but a shared CI egress address reaches the unauthenticated limit easily. A private fork also needs this token to read its own releases. The default is the workflow's own token, so it usually needs no thought.

### Outputs

| Output | What it is |
| ------ | ---------- |
| `version` | The version that was installed, resolved. |
| `path` | The installed binary's full path, `.exe` included on Windows. |

The `version` is the script's own stdout. This is why the scripts print the version there and everything else to standard error.

```yaml
- uses: yohimik/crier@v1
  id: crier
- run: echo "installed ${{ steps.crier.outputs.version }}"
- run: ${{ steps.crier.outputs.path }} --version
```

### `@v1` and the stable line

The `@v1` tag is a moving tag. It follows the newest 1.x **stable** release. Scoping it this way is deliberate. A user writing `@v1` asked for the stable line. A release candidate answering that request would hand them a prerelease they did not ask for.

So **`@v1` does not exist until the first stable release**. During the release-candidate period, pin the full tag:

```yaml
- uses: yohimik/crier@v1.0.0-rc.0
  with:
    version: 1.0.0-rc.0
```

Both halves are needed there. The ref chooses which action.yml runs. The `version` chooses what it installs. Without it, the script resolves the latest *stable* release. During that period, the latest stable release is nothing at all.

### Windows

You can use the same action on Windows. It runs `install.ps1` under `pwsh` instead of `install.sh`. The `path` output comes back with the `.exe`.

## go install

```sh
go install github.com/yohimik/crier/cmd/crier@latest
```

You need Go 1.26 or newer. The only difference is that the binary reports the module version instead of the one stamped at release time.

## From source

```sh
git clone https://github.com/yohimik/crier
cd crier
go build -o crier ./cmd/crier
```

Read [development](./development.md).

## Keeping it up to date

```sh
crier self-update            # replace this binary with the newest release
crier self-update --check    # is there a newer one? exit 1 if so, and change nothing
```

The download is verified against the sha256 digest GitHub publishes for the asset, the same digest the install script checks. The new binary is also run once before anything moves. Nothing is replaced until every check passes. A failed update leaves your working binary exactly where it was.

The tool keeps the old binary next to the new one as `crier.backup` (`crier.backup.exe` on Windows) for a week. Every later run removes it once it is older than a week. Within that week:

```sh
crier self-update --rollback
```

A rollback rotates the files instead of moving them. This makes the rollback reversible. If you run it twice, you return to where you started.

| Flag | What it does |
| ---- | ------------ |
| `--check` | report whether a newer release exists; exit 1 when not current |
| `--release <v>` | install that version rather than the newest |
| `--prerelease` | consider release candidates too |
| `--rollback` | put the binary the last update replaced back |
| `--api-url` | a GitHub Enterprise API, or a mirror |
| `--token-env` | the variable holding a GitHub token (default `GITHUB_TOKEN`) |
| `--json` | the report as JSON |

The tool only sends the token to github.com. If you point `--api-url` somewhere else, the token stays behind. A test mirror has no business receiving your GitHub credentials.

On Windows, the replacement uses two renames instead of a write. Windows refuses to overwrite a running executable, but it allows renaming it out of the way. The same two-rename process runs everywhere, keeping a single code path rather than two.

### A `go install` build updates differently

The `self-update` command refuses to replace a binary owned by the Go toolchain. It will tell you this directly:

```sh
go install github.com/yohimik/crier/cmd/crier@latest
```

## The asset names are a contract

A release contains six bare binaries:

```
crier-linux-amd64    crier-darwin-amd64    crier-windows-amd64.exe
crier-linux-arm64    crier-darwin-arm64    crier-windows-arm64.exe
```

Four separate tools resolve these names. Renaming an asset breaks all four at once:

- `install.sh` and `install.ps1`. These build the name from the detected platform.
- `dispat install yohimik/crier`.
- `crier self-update`. Its `AssetName` mirrors the same rule.
- The `Dockerfile` cross-compile loop. This is what produces the binaries.

There is no archive and no checksum file. GitHub publishes a sha256 digest for each asset. Every route above verifies the binary against this digest.

## Prerequisites

You do not need anything for images. You need **ffmpeg** for [video](../rendering/video.md). You need **ngrok** or **zrok** for a [tunnel](../staging/tunnels.md). You only need these tools when you use those features. The crier tool checks for both before it does any work.

## Verifying

```sh
crier --version
crier platforms      # what is configured, and what is missing
```
