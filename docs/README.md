# crier documentation

crier renders an HTML template to an image or a video and publishes it to nine
social platforms. Start with the [quickstart](../README.md); this is the
reference.

## Configuration

| Page | What it covers |
| ---- | -------------- |
| [Configuration](./configuration/README.md) | How the file, environment and flags compose, and how the file is found |
| [Reference](./configuration/reference/README.md) | Every key, generated from the code |
| [`crier.example.yaml`](../crier.example.yaml) | Every option with its default, in one file |

## Templates

| Page | What it covers |
| ---- | -------------- |
| [Writing templates](./templates/README.md) | The basics, and the data document |
| [Overlays](./templates/overlays.md) | One layout, a different shape per platform |
| [Pools and randomisation](./templates/pools.md) | Several layouts, seeded variation, reproducible runs |
| [Captions](./templates/captions.md) | Post text as a template, per platform |
| [Text overflow](./templates/text-overflow.md) | Keeping a long value inside its box |
| [Fonts](./templates/fonts.md) | System fonts, bundled fonts, hermetic rendering |

## Rendering

| Page | What it covers |
| ---- | -------------- |
| [Rendering](./rendering/README.md) | The render command, sizes, scale, formats |
| [CSS support](./rendering/css-support.md) | What the engine implements, and what it does not |
| [Video](./rendering/video.md) | ffmpeg, frame variables, codecs |

## Publishing

| Page | What it covers |
| ---- | -------------- |
| [Publishing](./publishing/README.md) | The pipeline, the fan-out, dry runs |
| [Instagram](./publishing/instagram.md) · [Facebook](./publishing/facebook.md) · [TikTok](./publishing/tiktok.md) · [Telegram](./publishing/telegram.md) · [X](./publishing/x.md) · [Mastodon](./publishing/mastodon.md) · [Discord](./publishing/discord.md) · [LinkedIn](./publishing/linkedin.md) · [Reddit](./publishing/reddit.md) | Getting the credentials, and each platform's quirks |

## Staging

| Page | What it covers |
| ---- | -------------- |
| [Staging](./staging/README.md) | Giving a rendered file a public URL |
| [Tunnels](./staging/tunnels.md) | Exposing a laptop to Instagram's fetcher |

## Operations

| Page | What it covers |
| ---- | -------------- |
| [Logging](./operations/logging.md) | Levels, formats, what goes where |
| [Exit codes](./operations/exit-codes.md) | What each code means, and how to branch on it |
| [Installing](./operations/install.md) | Every way to get the binary |
| [Development](./operations/development.md) | Building and testing crier itself |
| [Releasing](./operations/release.md) | The dispat flow, and the first release |
