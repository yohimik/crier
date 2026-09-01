# Rendering

```sh
crier render --render-template card.html --render-data card.yaml --render-output card.png
```

`crier render` prints the path it wrote to standard output and nothing else, so
a script can use it directly:

```sh
open "$(crier render)"
```

Logs go to standard error. `--json` prints a report instead:

```json
{ "variant": "base", "kind": "image", "files": ["card.png"], "width": 1080, "height": 1080 }
```

## Size

`render.width` and `render.height` are CSS pixels, and crier appends an
`@page { size: WpxHpx; margin: 0 }` rule **after** the document's own styles —
so a size given on the command line wins over one written in the template.

With both set to `0`, the document's own `@page` rule decides. A document that
lays out into more than one page is an error: whatever is being published is
one image, and silently posting page one is worse than saying so.

## Scale and supersampling

`render.scale` is the device pixel ratio — output pixels per CSS pixel. A
1080×1080 card at `--render-scale 2` is a 2160×2160 file, with the text and the
curves drawn at that resolution rather than upscaled.

`render.supersample` renders at a further multiple and shrinks the result. The
vector antialiasing is already good, so it is a knob rather than a default.

## Formats

`render.format` is `png` or `jpeg`.

- **PNG** keeps transparency and is the default.
- **JPEG** has no alpha, so transparent pixels are flattened onto
  `render.background` (white by default) at `render.jpeg-quality`.

When publishing, crier encodes the union of what the enabled platforms need:
Instagram takes JPEG and nothing else, so a PNG project publishing to Instagram
gets a JPEG as well — for Instagram alone.

## Variants

`--render-variant <platform>` renders what that platform would get, overlays
and size included. See [overlays](../templates/overlays.md).

## Where the file goes

`render.output` names the file. Without it, crier writes `crier-output.png` in
the working directory — the temporary directory it renders into is removed when
the run ends, so a path that no longer exists would not be useful to print.

## Next

- [CSS support](./css-support.md) — what the engine implements
- [Video](./video.md) — rendering a clip instead of a still

Configuration keys: [`render.*`](../configuration/reference/render.md).
