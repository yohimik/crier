# Video

```yaml
render:
  video:
    enabled: true
    fps: 30
    duration: 3s
```

The template is executed and laid out **once per frame**, and the frames are
streamed straight into ffmpeg, so memory stays at one frame however long the
clip is.

## ffmpeg

ffmpeg does the encoding and is a prerequisite crier does not bundle:

```sh
brew install ffmpeg          # macOS
sudo apt install ffmpeg      # Debian, Ubuntu
```

Its absence is checked before the first frame is rendered:

```
crier: ffmpeg was not found on PATH; install it, or set render.video.ffmpeg-bin
```

## Frame variables

```html
<div style="opacity: {{ .Video.Progress }}">fading in</div>
<div>{{ .Video.Frame }} of {{ .Video.Frames }}</div>
```

| Field | Meaning |
| ----- | ------- |
| `.Video.Frame` | the frame index, from 0 |
| `.Video.Frames` | how many frames in total |
| `.Video.Time` | seconds from the start |
| `.Video.Progress` | 0 to 1 across the clip |

The [random helpers](../templates/pools.md) draw once for the whole clip rather
than once per frame, so nothing flickers.

## How long

`render.video.frames` is exact; without it the count is `duration × fps`. Both
are capped, because a mistyped duration is a render that runs for hours.

## MP4 or GIF

`render.video.format` chooses which:

```yaml
render:
  video:
    enabled: true
    format: gif      # or mp4, the default
    fps: 12
    duration: 2s
```

Both come out of the same frame pipeline — the template is rendered once per
frame and streamed into ffmpeg — and only the encoder arguments differ.

A GIF gets a palette derived from its own frames:

```
-vf "split[s0][s1];[s0]palettegen=stats_mode=diff[p];[s1][p]paletteuse=..." -loop 0
```

The split is what lets one command both measure the clip and encode it.
ffmpeg's default palette is a fixed 256-colour table, and a card with a
gradient background comes out of that in visible bands.

A GIF has no audio track, so `render.video.audio` is ignored rather than
refused, and the codec preset does not apply.

Keep them short. A GIF is uncompressed between frames, so twelve frames a
second for two seconds at 1080×1080 is already several megabytes — and X will
not take one over 15MB.

## Output

`render.video.codec-preset` picks the encoder arguments:

| Preset | What it produces |
| ------ | ---------------- |
| `h264` (default) | `libx264`, `yuv420p`, `+faststart` |
| `h265` | `libx265`, `yuv420p`, `hvc1`, `+faststart` |
| `vp9` | `libvpx-vp9`, `yuv420p` |
| `none` | nothing; `render.video.ffmpeg-args` supplies everything |

`+faststart` puts the index at the front of the file. Instagram rejects a video
without it and nothing else minds, which is why it is on by default.

`render.video.ffmpeg-args` is appended before the output file, and
`render.video.audio` mixes in a track (`-c:a aac`, `-shortest`).

An odd width or height gets a `scale=trunc(iw/2)*2:trunc(ih/2)*2` filter, since
`yuv420p` subsamples chroma and has nowhere to put an odd column.

## What it costs

A layout pass per frame. Three seconds at thirty frames a second is ninety full
renders — measured in tens of seconds for a simple card, minutes for a heavy
one. Progress is logged at debug level every thirty frames, and the context is
checked between frames, so Ctrl-C stops it.

## Which platforms take which

| Platform | MP4 | GIF | How the GIF goes out |
| -------- | --- | --- | -------------------- |
| [Telegram](../publishing/telegram.md) | yes | yes | `sendAnimation`, not `sendVideo` |
| [Discord](../publishing/discord.md) | yes | yes | the file as it is |
| [Mastodon](../publishing/mastodon.md) | yes | yes | the v2 media endpoint, polled like a video |
| [X](../publishing/x.md) | yes | yes | chunked upload with `media_category=tweet_gif`, 15MB cap |
| [Reddit](../publishing/reddit.md) | yes | yes | the lease flow with `image/gif`, submitted as an image |
| [Instagram](../publishing/instagram.md) | yes | **no** | — |
| [Facebook](../publishing/facebook.md) | yes | **no** | — |
| [TikTok](../publishing/tiktok.md) | yes | **no** | — |
| [LinkedIn](../publishing/linkedin.md) | yes | **no** | — |

A GIF aimed at one of the four is a configuration error, named and refused
before anything is rendered:

```
crier: render.video.enabled is set but linkedin cannot post an animated GIF;
disable it, or set render.video.format
```

The four are not an oversight: their APIs have no animation path that does not
mean "upload an MP4", and crier producing an MP4 while the configuration says
`gif` would be worse than saying no.

## Publishing a video

Every platform crier ships can post video, and each has its own limits:
Telegram 50MB, Discord 10MB on a free server, X 512MB and 140 seconds. crier
checks the ones it knows before uploading, so a rejection arrives in a second
rather than after the upload.

Reddit requires a poster image with a video; crier renders frame 0 as a JPEG
and uploads it alongside, with no configuration. A GIF is submitted as an image
rather than as a video, so it needs none.

A platform that could not take video would be a configuration error found
before anything is rendered.

Configuration keys: [`render.video.*`](../configuration/reference/render-video.md).
