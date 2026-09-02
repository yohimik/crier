# Rendering

```sh
crier render --render-template card.html --render-data card.yaml --render-output card.png
```

`crier render` prints only the path it wrote to standard output. This means you can use it directly in a script:

```sh
open "$(crier render)"
```

Logs go to standard error. Use `--json` to print a report instead:

```json
{ "variant": "base", "kind": "image", "files": ["card.png"], "width": 1080, "height": 1080 }
```
## Size

`render.width` and `render.height` are CSS pixels. Crier appends an `@page { size: WpxHpx; margin: 0 }` rule **after** the document's own styles. This means a size given on the command line wins over one written in the template.

If both are set to `0`, the document's own `@page` rule decides. A document that lays out into more than one page is an error. Whatever is being published is one image. Silently posting page one is worse than saying so.

## Scale and supersampling

`render.scale` is the device pixel ratio: output pixels per CSS pixel. A 1080×1080 card at `--render-scale 2` is a 2160×2160 file. The text and curves are drawn at that resolution rather than upscaled.

`render.supersample` renders at a further multiple and shrinks the result. The vector antialiasing is already good, so it is a knob rather than a default.

## Formats

You can set `render.format` to `png` or `jpeg`.

- **PNG** keeps transparency. This is the default.
- **JPEG** has no alpha. It flattens transparent pixels onto `render.background` (white by default) at `render.jpeg-quality`.

When publishing, crier encodes the union of what the enabled platforms need. Instagram takes JPEG and nothing else. A PNG project publishing to Instagram gets a JPEG as well. This is for Instagram alone.

## Variants

Use `--render-variant <platform>` to render exactly what a platform gets. This includes the size and any overlays. See [overlays](../templates/overlays.md).

## Where the file goes

`render.output` names the file. Without it, crier writes `crier-output.png` in the working directory. The temporary directory it renders into is removed when the run ends. Printing a path that no longer exists would not be useful.

## Next

- [CSS support](./css-support.md): see what the engine implements.
- [Video](./video.md): read about rendering a clip instead of a still.

You can find the configuration keys at [`render.*`](../configuration/reference/render.md).
