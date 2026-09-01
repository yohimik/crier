# Overlays

One layout, a different shape per platform.

A base template marks its variable parts with `{{ block }}`:

```html
<div class="card">
  {{ block "headline" . }}<h1>{{ .title }}</h1>{{ end }}
  {{ block "body" . }}<p>{{ .subtitle }}</p>{{ end }}
</div>
```

An overlay file redefines any of them:

```html
{{/* story-overlay.html */}}
{{ define "headline" }}<h1 style="font-size:52px">{{ .shortTitle }}</h1>{{ end }}
```

An overlay file must contain nothing but `{{ define }}` blocks and whitespace.

## Where overlays come from

```yaml
render:
  template: base.html
  overlays: [brand.html]        # every platform gets this one

publish:
  instagram:
    overlay: [story.html]       # instagram also gets this one
    width: 1080
    height: 1920
  discord:
    width: 1200
    height: 630
```

They are parsed after the base, in order — global first, then the platform's
own — and the last definition of a name wins.

## Variants

A **variant** is one combination of overlays and size. Platforms that agree on
both share a variant and are rendered once:

- Instagram gets `base + brand + story`, at 1080×1920.
- Discord gets `base + brand`, at 1200×630.
- Everything else gets `base + brand`, at `render.width` × `render.height`.

Discord and "everything else" are two variants because the size differs; two
platforms with no overrides at all are one variant, and one render.

With no overlays and no per-platform sizes anywhere, there is exactly one
variant — which is the behaviour of a project that never heard of this page.

## Fitting the platform

An overlay reflows the layout for a platform. A **fit** reshapes what was
drawn. They answer the same question — how does one card become a story? — and
they answer it differently:

| | Overlay | Fit |
| --- | --- | --- |
| What changes | the layout is drawn again at the new size | the drawn image is resampled into the new size |
| Text | re-typeset for the shape | scaled with everything else |
| You write | a `{{ define }}` block | one line of configuration |
| Best for | a design that should be different | a design that should be the same, cropped |

Instagram is the motivating case. A story is 1080×1920, and Instagram crops
whatever it is given to that shape on its own servers, without saying where it
cut. Setting a fit does the cropping here, where you can see it:

```yaml
render:
  width: 1080
  height: 1080          # the card everything else gets

publish:
  instagram:
    width: 1080         # the frame the story is fitted into
    height: 1920
    fit: cover
  telegram:
    enabled: true       # no fit: the 1080 square, as it is
```

**When a fit is set, the platform's `width` and `height` stop being the render
size and become the target size.** The layout is drawn at `render.width` ×
`render.height` and then resampled. That is the whole point: reflowing a square
card into a tall box moves the text, and resampling keeps the design that was
approved.

### The modes

| `fit` | What it does |
| ----- | ------------ |
| `none` *(default)* | send the render as it is |
| `cover` | scale to fill the frame, crop the overflow, centred |
| `contain` | scale to fit inside the frame, fill the rest with the background |
| `stretch` | ignore the aspect ratio |

`cover` loses the edges and keeps the middle. `contain` loses nothing and shows
bars. `stretch` is almost always a mistake and occasionally exactly right.

### Choosing between cover and contain

Look at the numbers before choosing. A 1080 square into a 1080×1920 story
scales by 1.78 to fill the height, which pushes **44% of the width off each
side**. That is fine for a photographic card with its subject in the middle,
and ruinous for a card whose headline runs edge to edge — the headline loses
its first and last words.

So:

- **`cover`** when the card has margin to lose: a background image, a centred
  logo, text well inside the safe area.
- **`contain`** when the card is a layout: headlines, prices, buttons. The bars
  are honest, and setting `fit-background` to the card's own background colour
  makes them disappear into it.
- **An [overlay](#variants)** when the story deserves its own design. It costs
  a `{{ define }}` block and gives a layout made for the shape.

Render both and look:

```sh
crier render --render-variant instagram --render-output story.png
```

### The background

```yaml
    fit: contain
    fit-background: "#112233"
```

`publish.<platform>.fit-background` is a hex colour — `#000`, `#112233`,
`#11223344` — and defaults to white. It fills a `contain` letterbox, and it is
also what transparency is flattened onto, so a card with an alpha channel
arrives looking the way you chose rather than the way the platform chose.

### A fit needs a frame

`fit` anything but `none` requires the platform's `width` and `height`.
Without them there is nothing to scale towards, so crier refuses to start
rather than quietly sending the master:

```
crier: publish.instagram.fit is cover, which needs a frame to fit into:
set publish.instagram.width and publish.instagram.height (they are the target
size, not the render size, when a fit is set)
```

### Video and animated GIFs

The same three modes, done by ffmpeg rather than by crier — `scale` with
`force_original_aspect_ratio` and then `crop` or `pad`. The still poster that
accompanies a clip is fitted to match, so the frame a platform shows before
the video plays is the same shape as the video.

### Fits are part of the variant

Two platforms wanting the same layout at the same frame with the same fit share
one render *and* one encode. Two platforms agreeing about the layout and
disagreeing about the fit are two pictures, and get two.

## Previewing one

```sh
crier render --render-variant instagram
```

`crier --json` reports the variants it rendered and which platforms
each one served.

## Worked example

[`examples/video-game-release`](../../examples/video-game-release/) ships a
1920×1080 announcement and a 1080×1920 Instagram story from one
[`template.html`](../../examples/video-game-release/template.html) plus
[`story-overlay.html`](../../examples/video-game-release/story-overlay.html).

Configuration keys: [`render.overlays`](../configuration/reference/render.md)
and `publish.<platform>.{overlay,width,height,fit,fit-background}` on each
platform's reference page.
