# Writing templates

A crier template is a Go [`html/template`](https://pkg.go.dev/html/template)
file that produces an HTML document. crier lays that document out and paints it
into an image.

```sh
crier render --render-template card.html --render-data card.yaml --render-output card.png
```

## The data document

`render.data` says where the template's values come from. It takes three
forms:

| Value | Source |
| ----- | ------ |
| `card.yaml`, `card.json` | a file, parsed by its extension |
| `-` | standard input, parsed as YAML (which also accepts JSON) |
| `env:CARD_` | every `CARD_*` environment variable |

### A file, or standard input

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
./release-notes --json | crier --render-data -
```

The document is read **once** per run, which matters for video: a clip of
ninety frames does not read standard input ninety times.

### From the environment

`env:` followed by a prefix builds the document from the environment, so a
project needs no data file at all:

```yaml
# crier.yaml
render:
  template: card.html
  data: env:CARD_
```

```sh
CARD_TITLE="crier ships v1" CARD_SUBTITLE="One template, ten platforms." crier render
```

The mapping is deliberately dull — strip the prefix, lower-case what is left,
keep the underscores:

| Variable | Template |
| -------- | -------- |
| `CARD_TITLE` | `{{ .title }}` |
| `CARD_MAIN_TITLE` | `{{ .main_title }}` |
| `CARD_SUBTITLE` | `{{ .subtitle }}` |
| `CARDBOARD`, `OTHER_TITLE` | not matched |

The prefix is matched as written, so `CARD_` does not pick up `card_title`.

**Every value is a string, exactly as written.** No number, no boolean, no
date: a template overwhelmingly prints what it is given, and a value that
quietly became a number would render `1.0` where `"1.0"` was meant. Comparisons
in a template see strings too, so `{{ if eq .count "3" }}` rather than `3`.

**There is no nesting.** A flat namespace cannot say whether `CARD_MAIN_TITLE`
means `main.title` or `main_title`, so crier never guesses — it is always the
latter. Anything with structure in it — a list of tags, an object of theme
colours — belongs in a file, or on standard input:

```sh
./release-notes --json | crier --render-data -
```

That is not a limitation to work around; it is the reason both forms exist.

A prefix that matches nothing is not an error — the template renders with an
empty document — but it is warned about, because a card full of blanks with no
explanation is the worse outcome:

```
WRN no environment variables carry this prefix; the template will render with no data  prefix=CARD_
```

`env:` with no prefix after it **is** an error. It would match every variable
in the environment — the `PATH`, the shell, whatever CI exports — and hand the
lot to the template.

The captions see the same document the layout does, so one source covers the
whole run. It works in every mode, including
[publish-only](../publishing/flows.md#publish-only), where the template is not
rendered but the caption still is.

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
