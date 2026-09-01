# Writing templates

A crier template is a Go [`html/template`](https://pkg.go.dev/html/template)
file that produces an HTML document. crier lays that document out and paints it
into an image.

```sh
crier render --render-template card.html --render-data card.yaml --render-output card.png
```

## The data document

`render.data` is a JSON or YAML file, or `-` to read it from standard input:

```yaml
# card.yaml
title: crier ships v1
tags: [release, go]
theme:
  accent: "#4a2f6f"
```

```html
<h1>{{ .title }}</h1>
<p style="color: {{ .theme.accent }}">{{ join ", " .tags }}</p>
```

Reading from standard input is what makes crier scriptable — the program that
knows the numbers pipes them into the template that draws them:

```sh
./release-notes --json | crier publish --render-data -
```

The document is read **once** per run, which matters for video: a clip of
ninety frames does not read standard input ninety times.

## Functions

| Function | Example |
| -------- | ------- |
| `upper`, `lower`, `title`, `trim` | `{{ upper .tag }}` |
| `join` | `{{ join ", " .names }}` |
| `repeat` | `{{ repeat "─" 20 }}` |
| `default` | `{{ default "Untitled" .title }}` |
| `dict` | `{{ template "row" (dict "k" "Name" "v" .name) }}` |
| `now`, `date` | `{{ date "2 January 2006" now }}` |

The random helpers — `randChoice`, `randInt`, `randFloat`, `randShuffle` — are
in [pools and randomisation](./pools.md).

## Layout that works

The page is exactly `render.width` by `render.height`. A card that should fill
it needs the height carried all the way down:

```css
html, body { height: 100%; margin: 0 }
.card { width: 100%; height: 100%; box-sizing: border-box; padding: 80px }
```

Two things bite here.

A child's top margin **collapses out** of the card and takes the background
with it, which shows up as a white band along the top. Put the space on the
card as padding rather than on the child as a margin.

`height: 100%` on the card resolves against `body`, which resolves against
`html`, so both need the height or the card falls back to its content height.

Prefer block layout with explicit padding over flexbox — see
[CSS support](../rendering/css-support.md) for why.

## Next

- [Overlays](./overlays.md) — one layout, a different shape per platform
- [Pools and randomisation](./pools.md) — several layouts, seeded variation
- [Captions](./captions.md) — the post text is a template too
- [Text overflow](./text-overflow.md) — keeping a long value inside its box
- [Fonts](./fonts.md) — bundling your own, and reproducible output

Configuration keys: [`render.*`](../configuration/reference/render.md).
