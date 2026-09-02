# Video

```yaml
render:
  video:
    enabled: true
    fps: 30
    duration: 3s
```

The template is executed and laid out **once per frame**. These frames stream straight into ffmpeg. This keeps memory usage at one frame, no matter how long the clip is.

## ffmpeg

ffmpeg does the encoding. It is a prerequisite that crier does not bundle:

```sh
brew install ffmpeg          # macOS
sudo apt install ffmpeg      # Debian, Ubuntu
```

Crier checks if it is missing before rendering the first frame:

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

The [random helpers](../templates/pools.md) draw once for the whole clip instead of once per frame. This means nothing flickers.

## How long

`render.video.frames` is exact. Without it, the count is `duration × fps`. Both are capped. The cap exists because a mistyped duration means a render that runs for hours.

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

Both come out of the same frame pipeline. The template is rendered once per frame and streamed into ffmpeg. Only the encoder arguments differ.

A GIF gets a palette derived from its own frames:

```
-vf "split[s0][s1];[s0]palettegen=stats_mode=diff[p];[s1][p]paletteuse=..." -loop 0
```

This split lets one command both measure the clip and encode it. The default ffmpeg palette is a fixed 256-colour table. A card with a gradient background comes out of that table in visible bands.

A GIF has no audio track. The `render.video.audio` setting is ignored rather than refused, and the codec preset does not apply.

Keep them short. A GIF is uncompressed between frames. Twelve frames a second for two seconds at 1080×1080 is already several megabytes, and X will not take one over 15MB.

## Output

The `render.video.codec-preset` setting chooses the encoder arguments:

| Preset | What it produces |
| ------ | ---------------- |
| `h264` (default) | `libx264`, `yuv420p`, `+faststart` |
| `h265` | `libx265`, `yuv420p`, `hvc1`, `+faststart` |
| `vp9` | `libvpx-vp9`, `yuv420p` |
| `none` | nothing; `render.video.ffmpeg-args` supplies everything |

The `+faststart` flag puts the index at the front of the file. Instagram rejects a video without it. Nothing else minds. This is why it is on by default.

The `render.video.ffmpeg-args` value is appended before the output file. The `render.video.audio` setting mixes in a track (`-c:a aac`, `-shortest`). That track ends up inside the file, so every platform gets it. This is not the same as [attaching an audio file to a post](../publishing/music.md), which three platforms accept and the rest do not.

An odd width or height gets a `scale=trunc(iw/2)*2:trunc(ih/2)*2` filter. This is because `yuv420p` subsamples chroma. It has nowhere to put an odd column.

## What it costs

This requires a layout pass per frame. Three seconds at thirty frames a second is ninety full renders. This takes tens of seconds for a simple card, and minutes for a heavy one. Progress is logged at debug level every thirty frames. The context is checked between frames, so Ctrl-C stops it.

## Which platforms take which

| Platform | MP4 | GIF | How the GIF goes out |
| -------- | --- | --- | -------------------- |
| [Telegram](../publishing/telegram.md) | yes | yes | `sendAnimation`, not `sendVideo` |
| [Discord](../publishing/discord.md) | yes | yes | the file as it is |
| [Mastodon](../publishing/mastodon.md) | yes | yes | the v2 media endpoint, polled like a video |
| [X](../publishing/x.md) | yes | yes | chunked upload with `media_category=tweet_gif`, 15MB cap |
| [Reddit](../publishing/reddit.md) | yes | yes | the lease flow with `image/gif`, submitted as an image |
| [Slack](../publishing/slack.md) | yes | yes | a file like any other, played inline |
| [VK](../publishing/vk.md) | yes | yes | the document methods, because a saved photo would be a still |
| [Instagram](../publishing/instagram.md) | yes | **no** | none |
| [Facebook](../publishing/facebook.md) | yes | **no** | none |
| [TikTok](../publishing/tiktok.md) | yes | **no** | none |
| [LinkedIn](../publishing/linkedin.md) | yes | **no** | none |

Sending a GIF to one of these four is a configuration error. The system names the error and refuses it before anything is rendered:

```
crier: render.video.enabled is set but linkedin cannot post an animated GIF;
disable it, or set render.video.format
```

These four are not an oversight. Their APIs only support animations if you upload an MP4. If crier produced an MP4 while the configuration says `gif`, that would be worse than just saying no.

## Publishing a video

Every platform crier ships can post video. Each has its own limits: Telegram allows 50MB, Discord allows 10MB on a free server, and X allows 512MB and 140 seconds. crier checks known limits before uploading. This means a rejection arrives in a second instead of after the upload.

Reddit requires a poster image with a video. crier renders frame 0 as a JPEG and uploads it alongside. This requires no configuration. A GIF is submitted as an image instead of a video, so it needs no poster image.

If a platform cannot take video, it is a configuration error. crier finds this error before rendering anything.

Configuration keys: [`render.video.*`](../configuration/render/video.md).
