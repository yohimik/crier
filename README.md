# crier

Render an HTML template to an image or a video, and publish it to ten social
platforms with one command.

```sh
cd my-project && crier
```

One layout, one data file, one config — and Instagram gets a story, Discord
gets a card, and everyone gets the caption written for them.

crier finds the configuration by walking up from where you are, the way git
finds a repository, and publishing is what it does with no arguments. So the
everyday flow is: change directory, run `crier`.

- **Ten platforms.** Instagram, Facebook, TikTok, Telegram, X, Mastodon,
  Discord, LinkedIn, Reddit, Slack. Images, video and animated GIFs — and any
  shell script you like, as a [platform of your own](./docs/publishing/custom.md).
- **HTML and CSS you already know.** Gradients, web fonts, SVG, blend modes —
  laid out by a pure-Go engine, painted by a rasterizer written for it. No
  headless browser.
- **One layout, many shapes.** Template overlays and per-platform sizes, so one
  card becomes a story and a banner without a second template.
- **Configuration that composes.** Every value settable in a file, an
  environment variable and a flag; the file found by walking up from where you
  are, the way git finds a repository.
- **A single static binary.** `CGO_ENABLED=0`, six platforms, nothing to
  install alongside it.

## Install

### Install script

macOS and Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/yohimik/crier/main/install.sh | sh
```

Windows:

```powershell
irm https://raw.githubusercontent.com/yohimik/crier/main/install.ps1 | iex
```

Resolves the latest stable release, verifies the download against the sha256
digest GitHub publishes for it, and installs to `/usr/local/bin` when that is
writable or `~/.local/bin` otherwise. Pin a version with `CRIER_VERSION`, and
choose the directory with `CRIER_BIN_DIR`.

### dispat install

```sh
dispat install yohimik/crier --asset 'crier-{os}-{arch}'
```

`--asset` is **required**: a crier release carries six binaries, and a bare
`dispat install` only resolves when a release has exactly one.

Before the first stable release, add `--prerelease` — the release candidates
are prereleases, and `dispat install` skips those by default:

```sh
dispat install yohimik/crier --asset 'crier-{os}-{arch}' --prerelease
```

### go install

```sh
go install github.com/yohimik/crier/cmd/crier@latest
```

Builds from source and needs Go 1.26 or newer. The binary reports the module
version rather than the one stamped at release time — released binaries carry
the version, the commit and the build date in their ldflags, and a `go install`
build reads what it can from the module's own build info instead. Everything
else is identical.

`@latest` follows Go's prerelease rule and skips release candidates; name one
explicitly during the rc period.

### Keeping it up to date

```sh
crier self-update            # verified against GitHub's digest, and reversible
crier self-update --rollback # within a week of an update
```

More ways, and what each does: [installing](./docs/operations/install.md).

## Quickstart

```sh
mkdir promo && cd promo
crier init          # writes a commented crier.yaml to edit
```

`crier init` names a template and a data file. Write them:

`template.html`:

```html
<!doctype html>
<html><head><style>
  html, body { height: 100%; margin: 0 }
  body { font-family: "Go", sans-serif }
  .card {
    width: 100%; height: 100%; box-sizing: border-box; padding: 96px;
    background: linear-gradient(160deg, #12203a, #4a2f6f); color: #fff;
  }
  h1 { font-size: 96px; margin: 0 }
  p  { font-size: 40px; opacity: .8 }
</style></head>
<body><div class="card">
  <h1>{{ .title }}</h1>
  <p>{{ .subtitle }}</p>
</div></body></html>
```

`data.yaml`:

```yaml
title: crier ships v1
subtitle: One template, ten platforms, one command.
```

Then fill in `crier.yaml`, which `init` already wrote:

```yaml
render:
  template: template.html
  data: data.yaml
  width: 1080
  height: 1080
  output: card.png
  hermetic-fonts: true

publish:
  caption: "{{ .title }} — {{ .subtitle }}"
  telegram:
    enabled: true
    chat-id: "@my_channel"
```

Then:

```sh
export CRIER_PUBLISH_TELEGRAM_TOKEN=…

crier render      # 1. see the picture: card.png, no network
crier ping        # 2. are the credentials right? nothing is posted
crier --dry-run   # 3. what would be sent, still no network
crier             # 4. post it
```

Every option with its default is in
[`crier.example.yaml`](./crier.example.yaml) — all options at a glance, and
`crier init --full` writes the same thing into your own directory.

## Examples

Each one is a complete project: run the command, get the image. All previews
below were rendered by crier itself.

| | Example | Demonstrates |
| --- | --- | --- |
| <img src="./examples/business-promo/preview.png" width="150"> | **[business-promo](./examples/business-promo/)** · 1080×1080<br>[`template.html`](./examples/business-promo/template.html) · [`crier.yaml`](./examples/business-promo/crier.yaml)<br>`crier render --config examples/business-promo/crier.yaml` | Bundled font (Poppins), linear gradient, [line-clamp overflow](./docs/templates/text-overflow.md), caption templating |
| <img src="./examples/video-game-release/preview.png" width="150"> | **[video-game-release](./examples/video-game-release/)** · 1920×1080<br>[`template.html`](./examples/video-game-release/template.html) · [`story-overlay.html`](./examples/video-game-release/story-overlay.html) · [`crier.yaml`](./examples/video-game-release/crier.yaml)<br>`crier render --config examples/video-game-release/crier.yaml` | [Per-platform overlays](./docs/templates/overlays.md) — an Instagram story from the same layout — pixel display font, repeating gradient |
| <img src="./examples/social-quote/preview.png" width="150"> | **[social-quote](./examples/social-quote/)** · 1200×675<br>[`template-serif.html`](./examples/social-quote/template-serif.html) · [`template-panel.html`](./examples/social-quote/template-panel.html) · [`crier.yaml`](./examples/social-quote/crier.yaml)<br>`crier render --config examples/social-quote/crier.yaml` | [Template pool and seeded randomisation](./docs/templates/pools.md), serif face, radial gradient, alt text |
| <img src="./examples/release-changelog/preview.png" width="150"> | **[release-changelog](./examples/release-changelog/)** · 1080×1080<br>[`template.html`](./examples/release-changelog/template.html) · [`announce.sh`](./examples/release-changelog/announce.sh) · [`crier.yaml`](./examples/release-changelog/crier.yaml)<br>`crier render --config examples/release-changelog/crier.yaml` | A release posting about itself — [dispat](https://dispat.dev) release variables piped in on stdin, monospace face, scanline pattern |
| <img src="./examples/event-invite/preview.png" width="150"> | **[event-invite](./examples/event-invite/)** · 1080×1920<br>[`template.html`](./examples/event-invite/template.html) · [`crier.yaml`](./examples/event-invite/crier.yaml)<br>`crier render --config examples/event-invite/crier.yaml` | Story format, rounded face, duotone gradient |
| <img src="./examples/custom-platform/preview.png" width="150"> | **[custom-platform](./examples/custom-platform/)** · 1080×1080<br>[`template.html`](./examples/custom-platform/template.html) · [`publish.sh`](./examples/custom-platform/publish.sh) · [`crier.yaml`](./examples/custom-platform/crier.yaml)<br>`crier render --config examples/custom-platform/crier.yaml` | [A shell script as a platform](./docs/publishing/custom.md) — curl to a webhook, geometric sans over mono, layered radial background |
| <img src="./examples/square-1080/preview.png" width="150"> | **[square-1080](./examples/square-1080/)** · 1080×1080<br>[`template.html`](./examples/square-1080/template.html) · [`crier.yaml`](./examples/square-1080/crier.yaml)<br>`crier render --config examples/square-1080/crier.yaml` | The smallest useful project: no bundled fonts, no overlays |
| <img src="./examples/story-1080x1920/preview.png" width="150"> | **[story-1080x1920](./examples/story-1080x1920/)** · 1080×1920<br>[`template.html`](./examples/story-1080x1920/template.html) · [`crier.yaml`](./examples/story-1080x1920/crier.yaml)<br>`crier render --config examples/story-1080x1920/crier.yaml` | A story starter with `{{ block }}` sections ready for overlays |

The fonts the examples bundle are OFL-licensed and live in
[`examples/fonts/`](./examples/fonts/), each with its licence.

## Commands

| | |
| --- | --- |
| `crier` | render and post to every enabled platform — publishing is the default |
| `crier render` | render the template and write the file |
| `crier ping` | check every enabled platform's credentials, without posting |
| `crier platforms` | which platforms are enabled, and which are configured |
| `crier config` | the resolved configuration, secrets redacted |
| `crier init` | write a configuration file to start from (`--full` for every option) |
| `crier self-update` | replace this binary with the newest release (`--rollback` undoes it) |
| `crier --version` | the version, the commit and the build date |

`crier publish` spells the default out; `crier --dry-run` and every other flag
work without it. The full list, and the dispatch rule, is in
[the command line](./docs/operations/cli.md).

Results go to standard output, logs to standard error, and the
[exit code](./docs/operations/exit-codes.md) says what happened.

## Documentation

[The index](./docs/README.md) links everything. The pages people reach for
first:

- [Configuration](./docs/configuration/README.md) — how the three layers
  compose, and how the file is found
- [Writing templates](./docs/templates/README.md) · [overlays](./docs/templates/overlays.md) · [captions](./docs/templates/captions.md) · [text overflow](./docs/templates/text-overflow.md) · [fonts](./docs/templates/fonts.md)
- [CSS support](./docs/rendering/css-support.md) — what the engine implements
- [Publishing](./docs/publishing/README.md), and a page per platform
- [Staging](./docs/staging/README.md) — the public URL Instagram insists on
- [Configuration reference](./docs/configuration/reference/README.md) — every key

## Requirements

Nothing for images. [ffmpeg](./docs/rendering/video.md) for video, and
[ngrok or zrok](./docs/staging/tunnels.md) for a tunnel — both only when those
features are used.

## Changelog

[`cmd/crier/CHANGELOG.md`](./cmd/crier/CHANGELOG.md), written by
[dispat](https://dispat.dev) from the conventional commits. See
[releasing](./docs/operations/release.md).

## Contributing

[Development](./docs/operations/development.md) covers building, the two test
suites, the golden images and the commit convention.

## Licence

MIT. See [LICENSE](./LICENSE).
