# CSS support

crier lays documents out with
[webrender](https://github.com/benoitkugler/webrender), a pure-Go port of
WeasyPrint's engine, and paints them through a rasterizer written for this
project. What that gives you is roughly WeasyPrint's coverage.

The version is pinned exactly (`v0.0.14`), because the library is pre-1.0.

## Solid

Block and inline layout. Tables. `position: absolute` and `relative`. Floats.
`border-radius`, borders, outlines, box shadows. Background colours and images,
`background-repeat`, `background-size`, `background-position`. Linear and
radial gradients, repeating included. `opacity`. `transform`. `@font-face`,
`@page`, `@media`. SVG. The whole text model: `text-decoration`,
`letter-spacing`, `word-spacing`, `text-transform`, `hyphens`, bidirectional
text, and the [overflow recipes](../templates/text-overflow.md).

`mix-blend-mode` works for the eleven separable modes: `multiply`, `screen`,
`overlay`, `darken`, `lighten`, `color-dodge`, `color-burn`, `hard-light`,
`soft-light`, `difference`, `exclusion`.

## Simple cases only

**Flexbox** and **grid**. A single row of items usually works; a
`flex-direction: column` centring layout frequently does not — items overlap,
or the available width is computed wrongly and text wraps far too early.

Use block layout with explicit padding. Every example in this repository does,
and each one is a full-bleed card. See [writing
templates](../templates/README.md#layout-that-works).

## Not supported

| What | What happens |
| ---- | ------------ |
| JavaScript | Not executed. There is no script engine. |
| `-webkit-` anything | Ignored, with a warning naming the property. |
| `filter` | Ignored. |
| `mix-blend-mode: hue`, `saturation`, `color`, `luminosity` | Drawn normally, warned about once. They mix colour channels together and the compositor is per-channel. |
| Colour emoji in COLR or SVG format | Blank. Bitmap emoji (CBDT, sbix) do render. |

## Finding out

Anything the engine cannot use is logged at warning level:

```sh
crier render --log-level warn
```

```
WRN Ignored `-webkit-line-clamp:2` , prefixed selectors are ignored. from=webrender
```

That is the fastest way to find out why a template does not look the way it
does in a browser.

## Why a rasterizer of its own

webrender emits drawing operations against a backend interface; the only
maintained implementation writes PDF. crier's `internal/raster` implements that
interface against an RGBA image: paths, clips, glyph outlines, images,
gradients, groups, blend modes. It is exercised by golden images with a
tolerance for the float32 differences between architectures.
