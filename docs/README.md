# crier documentation

crier renders an HTML template to an image or a video. It publishes this to twelve social platforms. Start with the [quickstart](../README.md). This is the reference.

## Configuration

| Page | What it covers |
| ---- | -------------- |
| [Configuration](./configuration/README.md) | How the file, environment and flags compose, how the file is found, and every key |
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
| [Pagination and carousels](./rendering/pagination.md) | Long content across several pages, and the posts they become |
| [Video](./rendering/video.md) | ffmpeg, frame variables, codecs |

## Publishing

| Page | What it covers |
| ---- | -------------- |
| [Publishing](./publishing/README.md) | The pipeline, the fan-out, dry runs |
| [Flows](./publishing/flows.md) | The four ways into the pipeline: full, render-only, publish-only, encode-only |
| [Instagram](./publishing/instagram.md) · [Facebook](./publishing/facebook.md) · [TikTok](./publishing/tiktok.md) · [Telegram](./publishing/telegram.md) · [X](./publishing/x.md) · [Mastodon](./publishing/mastodon.md) · [Discord](./publishing/discord.md) · [LinkedIn](./publishing/linkedin.md) · [Reddit](./publishing/reddit.md) · [Slack](./publishing/slack.md) · [VK](./publishing/vk.md) · [Threads](./publishing/threads.md) | Getting the credentials, and each platform's quirks |
| [Music](./publishing/music.md) | Attaching an audio file, opening a post with a video, and why no API names a licensed track |
| [Custom platforms](./publishing/custom.md) | Any shell script as a platform, and the environment contract |

## Staging

| Page | What it covers |
| ---- | -------------- |
| [Staging](./staging/README.md) | Giving a rendered file a public URL |
| [Tunnels](./staging/tunnels.md) | Exposing a laptop to Instagram's fetcher |

## Operations

| Page | What it covers |
| ---- | -------------- |
| [The command line](./operations/cli.md) | Every command, the dispatch rule, `crier init` and `crier ping` |
| [Logging](./operations/logging.md) | Levels, formats, what goes where |
| [Exit codes](./operations/exit-codes.md) | What each code means, and how to branch on it |
| [Installing](./operations/install.md) | Every way to get the binary |
| [Development](./operations/development.md) | Building and testing crier itself |
| [Releasing](./operations/release.md) | The dispat flow, and the first release |
