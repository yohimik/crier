# CSS support

crier lays out documents with [webrender](https://github.com/benoitkugler/webrender). This library is a pure-Go port of WeasyPrint's engine. crier then paints them through a rasterizer written for this project. This gives you roughly WeasyPrint's coverage.

The version is pinned exactly (`v0.0.14`) because the library is pre-1.0.

## Solid

Block and inline layout, tables, and floats all work. You can use `position: absolute` and `relative`. For styling: `border-radius`, borders, outlines, and box shadows. You can set background colours and images, along with `background-repeat`, `background-size`, and `background-position`. Linear and radial gradients work, including repeating gradients. You can also use `opacity` and `transform`. `@font-face`, `@page`, `@media`, and SVG are supported, along with the whole text model. This includes `text-decoration`, `letter-spacing`, `word-spacing`, `text-transform`, `hyphens`, bidirectional text, and the [overflow recipes](../templates/text-overflow.md).

`mix-blend-mode` works for the eleven separable modes. These are `multiply`, `screen`, `overlay`, `darken`, `lighten`, `color-dodge`, `color-burn`, `hard-light`, `soft-light`, `difference`, and `exclusion`.

## Simple cases only

**Flexbox** and **grid**. A single row of items usually works. A `flex-direction: column` centring layout frequently does not. Items overlap, or the available width is computed wrongly and text wraps far too early.

Use block layout with explicit padding. Every example in this repository does. Each one is a full-bleed card. See [writing templates](../templates/README.md#layout-that-works).

## Not supported

| What | What happens |
| ---- | ------------ |
| JavaScript | Not executed. There is no script engine. |
| `-webkit-` anything | Ignored, with a warning naming the property. |
| `filter` | Ignored. |
| `mix-blend-mode: hue`, `saturation`, `color`, `luminosity` | Drawn normally, warned about once. They mix colour channels together and the compositor is per-channel. |
| Colour emoji in COLR or SVG format | Blank. Bitmap emoji (CBDT, sbix) do render. |


## Finding out

The engine logs anything it cannot use at the warning level:

```sh
crier render --log-level warn
```

```
WRN Ignored `-webkit-line-clamp:2` , prefixed selectors are ignored. from=webrender
```

This is the fastest way to find out why a template does not look like it does in a browser.

## Why a rasterizer of its own

webrender emits drawing operations against a backend interface. The only maintained implementation writes PDF. crier's `internal/raster` implements that interface against an RGBA image. It supports paths, clips, glyph outlines, images, gradients, groups, and blend modes. It is tested with golden images, with a tolerance for the float32 differences between architectures.
