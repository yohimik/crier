# Template pools and randomisation

A post that looks the same every week stops being read. crier can vary the
layout and the details, and still reproduce any run exactly.

## A pool of layouts

Set `render.pool` instead of `render.template` and crier picks one per run:

```yaml
render:
  pool:
    - template-serif.html
    - template-panel.html
```

```
INF picked a template from the pool template=template-panel.html pool=2
```

The pick is made **once** per run, so every platform variant and every video
frame uses the same layout.

## Random values inside a template

These are available in HTML templates **and** in [caption
templates](./captions.md):

| Function | Example | Result |
| -------- | ------- | ------ |
| `randChoice` | `{{ randChoice .accents }}` or `{{ randChoice "#f00" "#0f0" }}` | one item |
| `randInt` | `{{ randInt 100 260 }}` | a whole number in `[min,max]` |
| `randFloat` | `{{ randFloat 0.8 1.2 }}` | a decimal in `[min,max)` |
| `randShuffle` | `{{ range randShuffle .items }}` | the list, reordered |
| `randSeed` | `{{ randSeed }}` | this run's seed |

```css
{{ $accent := randChoice .accents }}
.card {
  border-left: 18px solid {{ $accent }};
  background: linear-gradient({{ randInt 100 260 }}deg, #2b1d16, #4a2f22);
}
```

Assign to a variable when the same value is needed twice: calling `randChoice`
again draws again.

## The seed

Every run draws from one seeded source, and the seed is **always** logged:

```
INF template randomisation seed seed=20260901
```

Pass it back to reproduce the run:

```sh
crier render --render-seed 20260901
```

Pin it in the configuration when a committed preview has to stay stable:

```yaml
render:
  seed: 20260901     # 0, the default, draws a new one each run
```

One seed covers the pool pick, every random function, every platform variant
and every video frame — so a clip does not flicker, and two platforms do not
disagree about the accent colour.

## Worked example

[`examples/social-quote`](../../examples/social-quote/) has a pool of two
layouts and a random accent, with the seed pinned in
[`crier.yaml`](../../examples/social-quote/crier.yaml) so its committed preview
is reproducible.

Configuration keys: [`render.pool` and
`render.seed`](../configuration/reference/render.md).
