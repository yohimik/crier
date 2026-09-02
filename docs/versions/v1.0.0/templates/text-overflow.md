# Text overflow

Data is data. A title that fits today might be twice as long tomorrow. Every recipe here is verified by a test in `internal/render/overflow_test.go`. This means the renderer does exactly what is written here.

## One line, with an ellipsis

```css
.title {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
```

You need all three properties. `white-space: nowrap` stops the text from wrapping. `overflow: hidden` stops the spill. `text-overflow: ellipsis` marks the cut.

## Several lines, with an ellipsis

The CSS Overflow Module's line clamp is the one to reach for on a body of text. It stops at a line count rather than slicing a line in half.

```css
.blurb {
  overflow: hidden;
  overflow-wrap: break-word;   /* so one long word cannot defeat it */
  max-lines: 3;
  continue: discard;
  block-ellipsis: auto;        /* or block-ellipsis: "…" */
}
```

## Break words longer than the box

Consider a URL, a hash, or a German compound noun. Without this, they are laid out as one unbreakable run and spill out of the box sideways.

```css
.body { overflow: hidden; overflow-wrap: break-word }
```

`word-break: break-all` is also honoured. It breaks text more aggressively: anywhere on the line, rather than only where a word would otherwise overflow.

## Just cut it

```css
.fixed { overflow: hidden }
```

`overflow: hidden` clips in both directions. This is why it is the base of every recipe above.

## Not supported

| What | Instead |
| ---- | ------- |
| `-webkit-line-clamp`, `display: -webkit-box` | `max-lines` + `continue: discard`. Prefixed properties are ignored with a warning. |
| `text-overflow: ellipsis` on a wrapping block | It needs `white-space: nowrap`. For several lines, use the clamp. |
| `line-clamp` as a shorthand | Write the three longhands; they are what the tests cover. |


## The real defence

Give the box a size and pick a recipe. Let long values truncate cleanly instead of breaking your layout.

[`examples/business-promo`](../../examples/business-promo/) uses a deliberately over-long blurb for this exact reason. See its [`template.html`](../../examples/business-promo/template.html).
