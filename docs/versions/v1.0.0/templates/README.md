# Writing templates

A crier template is a Go [`html/template`](https://pkg.go.dev/html/template) file. It produces an HTML document. Then, crier lays out that document and paints it into an image.

```sh
crier render --render-template card.html --render-data card.yaml --render-output card.png
```
## The data document

`render.data` sets where the template's values come from. It takes three forms:

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

Reading from standard input makes crier scriptable. The program that knows the numbers pipes them into the template that draws them:

```sh
./release-notes --json | crier --render-data -
```

The document is read **once** per run. This matters for video. A clip of ninety frames does not read standard input ninety times.

### From the environment

Use `env:` followed by a prefix to build the document from the environment. This means a project needs no data file at all:

```yaml
# crier.yaml
render:
  template: card.html
  data: env:CARD_
```

```sh
CARD_TITLE="crier ships v1" CARD_SUBTITLE="One template, fourteen platforms." crier render
```

The mapping is deliberately dull: strip the prefix, lower-case what is left, and keep the underscores:

| Variable | Template |
| -------- | -------- |
| `CARD_TITLE` | `{{ .title }}` |
| `CARD_MAIN_TITLE` | `{{ .main_title }}` |
| `CARD_SUBTITLE` | `{{ .subtitle }}` |
| `CARDBOARD`, `OTHER_TITLE` | not matched |

The prefix is matched as written. This means `CARD_` does not pick up `card_title`.

**Every value is a string, exactly as written.** There are no numbers, booleans, or dates. A template mostly prints what it is given. A value that quietly becomes a number would render `1.0` where `"1.0"` was meant. Comparisons in a template see strings too. You must write `{{ if eq .count "3" }}` rather than `3`.

**There is no nesting.** A flat namespace cannot say whether `CARD_MAIN_TITLE` means `main.title` or `main_title`. Crier never guesses. It always chooses the latter. Anything with structure, like a list of tags or an object of theme colours, belongs in a file or on standard input:

```sh
./release-notes --json | crier --render-data -
```

This is not a limitation to work around. It is the reason both forms exist.

A prefix that matches nothing is not an error. The template renders with an empty document. Crier warns you about this, because a card full of blanks with no explanation is the worse outcome:

```
WRN no environment variables carry this prefix; the template will render with no data  prefix=CARD_
```

Using `env:` with no prefix after it **is** an error. It would match every variable in the environment, like the `PATH`, the shell, or whatever CI exports. It would then hand everything to the template.

The captions see the same document as the layout. One source covers the whole run. This works in every mode, including [publish-only](../publishing/flows.md#publish-only). In this mode, the template is not rendered, but the caption still is.

## Functions

| Function | Example |
| -------- | ------- |
| `upper`, `lower`, `title`, `trim` | `{{ upper .tag }}` |
| `join` | `{{ join ", " .names }}` |
| `repeat` | `{{ repeat "─" 20 }}` |
| `default` | `{{ default "Untitled" .title }}` |
| `dict` | `{{ template "row" (dict "k" "Name" "v" .name) }}` |
| `now`, `date` | `{{ date "2 January 2006" now }}` |

The random helpers are in [pools and randomisation](./pools.md). These are `randChoice`, `randInt`, `randFloat`, and `randShuffle`.

## Layout that works

The page is exactly `render.width` by `render.height`. To make a card fill this space, you need to carry the height all the way down:

```css
html, body { height: 100%; margin: 0 }
.card { width: 100%; height: 100%; box-sizing: border-box; padding: 80px }
```

Watch out for two things here.

A child's top margin **collapses out** of the card. This takes the background with it and leaves a white band at the top. Put the space on the card as padding instead of on the child as a margin.

Setting `height: 100%` on the card resolves against `body`. The `body` resolves against `html`. Both need the height set. Otherwise, the card falls back to its content height.

Prefer block layout with explicit padding over flexbox. See [CSS support](../rendering/css-support.md) for why.

## Next

- [Overlays](./overlays.md): one layout, a different shape per platform
- [Pools and randomisation](./pools.md): several layouts, seeded variation
- [Captions](./captions.md): the post text is a template too
- [Text overflow](./text-overflow.md): keeping a long value inside its box
- [Fonts](./fonts.md): bundling your own, and reproducible output

Configuration keys: [`render.*`](../configuration/render/README.md).
