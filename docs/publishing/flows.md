# Flows

The pipeline for crier has four steps: template, render, encode, and publish. It also has four entrances. A poster somebody drew in Figma is a perfectly good thing to publish. Frames a simulation wrote are a perfectly good thing to encode. Neither is a second-class path.

| Mode | Selected by | What runs |
| ---- | ----------- | --------- |
| **Full** | the default | template → render → encode → stage → publish |
| **Render-only** | `crier render` | template → render → encode → a file |
| **Publish-only** | `publish.input` | an existing file → stage → publish |
| **Encode-only** | `render.video.frames-input` | existing frames → ffmpeg → publish |

The last two are mutually exclusive with each other. They are also mutually exclusive with `render.video.enabled`. Providing two answers to "where does the artifact come from" creates a configuration where the author believed two different things. Picking one answer would hide that.

```
crier: publish.input and render.video.frames-input both name where the
artifact comes from; set one
```

## Full

This is the default. The rest of the documentation describes it.

```sh
crier
```

## Render-only

This step stops after the encode and writes `render.output`. It does not use the network at all. This makes it the ideal loop to work in while a template takes shape.

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

The tool does not read a template. It lays out nothing. It does not involve ffmpeg. Staging, the fan-out, the captions and the exit codes all work exactly as they do for a rendered post.

**The file is identified by its bytes, not its name.** crier knows four formats: PNG, JPEG, GIF and MP4. It refuses anything else by name. If a file named `.png` holds a JPEG, crier uploads it as a JPEG. Platforms check the content type. If the type does not match, they reject the file with a message about something else entirely.

**Images are transcoded when a platform insists.** Instagram will not fetch a PNG. If you provide a PNG, crier re-encodes it as a JPEG for Instagram. It logs this action. Every other platform still gets the original file:

```
INF transcoded the input for a platform that needs it from=png to=jpeg
```

**Clips are passed through as they are.** A publish command should not surprise you by re-encoding your video to satisfy a format preference. If a platform cannot accept the clip format, crier treats it as a configuration error. It finds this error before uploading anything.

**Reddit needs a poster.** A rendered clip has a frame 0 to encode. A clip taken from disk does not. To fix this, crier uses ffmpeg to pull the first frame out of the file. You must have ffmpeg on your `PATH`. If you do not, crier refuses the combination before uploading anything. The error names the platform and the file.

Per-platform overlays and sizes do not apply. These are instructions for the renderer, and there is nothing to render. Every platform gets the one file.

Running `crier render` with `publish.input` set is a config error. There is nothing to render.

A project's `crier.yaml` may still name a template. The `--publish-input` flag simply wins. It logs this at the info level. This is deliberate. Running `crier --publish-input poster.png` inside a project is a normal thing to do. If this were an error, you would have to unset the template first.

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

The frames come from wherever you made them. You might use a simulation, Blender, or a screen recording split with ffmpeg. They go through the same encoder the rendered path uses. This means `render.video.format`, `codec-preset`, `ffmpeg-args` and `audio` all mean what they mean elsewhere.

`frames-input` is a directory or a glob:

```yaml
    frames-input: ./frames             # every png/jpg/gif in it
    frames-input: ./out/frame-*.png    # a glob
```

**The order is lexicographic**, so pad the numbers. For example, `frame-0001.png` through `frame-0090.png` sorts correctly. Files named `frame-1.png` through `frame-90.png` do not.

**Every frame has to be the same size.** The first frame decides this size. If another frame disagrees, it is named:

```
crier: f-0007.png is 1081x1080 but the first frame is 1080x1080;
every frame has to be the same size
```

Frames are decoded one at a time rather than all at once. Holding ninety 1080-square images in memory while ffmpeg reads them wastes a gigabyte for no reason.

The first frame doubles as the poster image for [the platforms that need one](../rendering/video.md#publishing-a-video).

Configuration keys: [`publish.input`](../configuration/reference/publish.md) and [`render.video.frames-input`](../configuration/reference/render-video.md).
