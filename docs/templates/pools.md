# Template pools and randomisation

People stop reading a post if it looks the same every week. You can use crier to vary the layout and the details. It will still reproduce any run exactly.

## A pool of layouts

Set `render.pool` instead of `render.template`. Crier will then pick one layout per run:

```yaml
render:
  pool:
    - template-serif.html
    - template-panel.html
```

```
INF picked a template from the pool template=template-panel.html pool=2
```

Crier makes this choice **once** per run. This means every platform variant and every video frame uses the same layout.

Naming a `render.template` as well is not an error. The pool wins and the single template is ignored. Say one or the other, so the file reads as one intention rather than two.

## A pool of soundtracks

A [clip](../rendering/video.md) can vary the same way. Set `render.video.audio-pool` instead of `render.video.audio`:

```yaml
render:
  video:
    audio-pool:
      - anthem.mp3
      - fanfare.mp3
      - galop.mp3
```

```
INF picked an audio track from the pool audio=fanfare.mp3 pool=3
```

Same rules as the layout pool, because it is the same machinery. One pick per run, from the same seeded source, so a clip's music does not disagree with its card. An empty pool leaves `render.video.audio` alone. Paths in a configuration file resolve beside the file, as every path does.

Setting both `render.video.audio` and a pool is not an error: the pool wins, exactly as `render.pool` wins over `render.template`. Say one or the other, so the file reads as one intention rather than two.

The pick is drawn on every run, whether or not the run renders a video. That is deliberate. A project whose release script calls crier several times over, a clip here and a still there, needs every call to draw in the same order. Otherwise one seed stops meaning one release. `announce/` is that project; see [the release notes page](../operations/release.md).

## Random values inside a template

You can use these in HTML templates **and** in [caption templates](./captions.md):

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

Assign the result to a variable if you need the same value twice. Calling `randChoice` again draws a new value.

## The seed

Every run draws from one seeded source. The seed is **always** logged:

```
INF template randomisation seed seed=20260901
```

Pass it back to reproduce the run:

```sh
crier render --render-seed 20260901
```

Pin it in the configuration to keep a committed preview stable:

```yaml
render:
  seed: 20260901     # 0, the default, draws a new one each run
```

One seed covers the pool pick, every random function, every platform variant and every video frame. This means a clip does not flicker, and two platforms do not disagree about the accent colour.

## Worked example

Take a look at [`examples/social-quote`](../../examples/social-quote/). It has a pool of two layouts and a random accent. The seed is pinned in [`crier.yaml`](../../examples/social-quote/crier.yaml). This makes its committed preview reproducible.

Configuration keys: [`render.pool` and `render.seed`](../configuration/render/README.md), and [`render.video.audio-pool`](../configuration/render/video.md).
