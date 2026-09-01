# Flows

crier's pipeline is four steps — template, render, encode, publish — and it has
four entrances. A poster somebody drew in Figma is a perfectly good thing to
publish, and frames a simulation wrote are a perfectly good thing to encode, so
neither is a second-class path.

| Mode | Selected by | What runs |
| ---- | ----------- | --------- |
| **Full** | the default | template → render → encode → stage → publish |
| **Render-only** | `crier render` | template → render → encode → a file |
| **Publish-only** | `publish.input` | an existing file → stage → publish |
| **Encode-only** | `render.video.frames-input` | existing frames → ffmpeg → publish |

The last two are mutually exclusive with each other and with
`render.video.enabled`: two answers to "where does the artifact come from" is a
configuration whose author believed two different things, and picking one would
hide that.

```
crier: publish.input and render.video.frames-input both name where the
artifact comes from; set one
```

## Full

The default, and what the rest of the documentation describes.

```sh
crier
```

## Render-only

Stops after the encode and writes `render.output`. No network at all, which is
what makes it the loop to work in while a template is taking shape.

```sh
crier render
crier render --render-output card.png
```

## Publish-only

```yaml
publish:
  input: ./card.png
  caption: "the card I made earlier"
```

```sh
crier --publish-input ./card.png
CRIER_PUBLISH_INPUT=./card.png crier
```

No template is read, nothing is laid out, and ffmpeg is not involved. Staging,
the fan-out, the captions and the exit codes all work exactly as they do for a
rendered post.

**The file is identified by its bytes, not its name.** PNG, JPEG, GIF and MP4
are the four crier knows; anything else is refused by name. A file called
`.png` that holds a JPEG is uploaded as a JPEG, because the platforms that
check the content type reject a mismatch with a message about something else
entirely.

**Images are transcoded when a platform insists.** Instagram will not fetch a
PNG, so a PNG input is re-encoded as a JPEG for it — logged, and the original
is still what every other platform gets:

```
INF transcoded the input for a platform that needs it from=png to=jpeg
```

**Clips are passed through as they are.** Re-encoding somebody's video to
satisfy a format preference would be a surprising thing for a publish command
to do, so a platform that cannot take the clip's kind is a configuration error
found before anything is uploaded.

Per-platform overlays and sizes do not apply: they are instructions to the
renderer, and there is nothing to render. Every platform gets the one file.

`crier render` with `publish.input` set is a config error — there is nothing to
render.

A project's `crier.yaml` may still name a template; `--publish-input` simply
wins, and says so at info level. That is deliberate: running
`crier --publish-input poster.png` inside a project is an ordinary thing to do,
and making it an error would mean unsetting the template first.

## Encode-only

```yaml
render:
  video:
    format: gif
    fps: 12
    frames-input: ./frames        # a directory, or a glob
```

```sh
crier                              # encode and post
crier render --render-output out.gif   # encode and stop
```

The frames come from wherever you made them — a simulation, Blender, a
screen recording split with ffmpeg — and go through the same encoder the
rendered path uses, so `render.video.format`, `codec-preset`, `ffmpeg-args` and
`audio` all mean what they mean elsewhere.

`frames-input` is a directory or a glob:

```yaml
    frames-input: ./frames             # every png/jpg/gif in it
    frames-input: ./out/frame-*.png    # a glob
```

**The order is lexicographic**, so pad the numbers: `frame-0001.png` through
`frame-0090.png` sorts correctly and `frame-1.png` through `frame-90.png` does
not.

**Every frame has to be the same size.** The first frame decides it, and one
that disagrees is named:

```
crier: f-0007.png is 1081x1080 but the first frame is 1080x1080;
every frame has to be the same size
```

Frames are decoded one at a time rather than all at once: ninety 1080-square
images held in memory while ffmpeg reads them one by one is a gigabyte for no
reason.

The first frame doubles as the poster image, for
[the platforms that need one](../rendering/video.md#publishing-a-video).

Configuration keys: [`publish.input`](../configuration/reference/publish.md) and
[`render.video.frames-input`](../configuration/reference/render-video.md).
