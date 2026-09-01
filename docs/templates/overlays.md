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
and `publish.<platform>.{overlay,width,height}` on each platform's reference
page.
