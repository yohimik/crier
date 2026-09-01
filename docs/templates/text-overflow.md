# Text overflow

Data is data: a title that fits today is twice as long tomorrow. Every recipe
here is verified by a test in `internal/render/overflow_test.go`, so what is
written here is what the renderer does.

## One line, with an ellipsis

```css
.title {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
```

`white-space: nowrap` stops the wrap, `overflow: hidden` stops the spill, and
`text-overflow: ellipsis` marks the cut. All three are needed.

## Several lines, with an ellipsis

The CSS Overflow Module's line clamp — the one to reach for on a body of text,
because it stops at a line count rather than slicing a line in half.

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

A URL, a hash, a German compound noun. Without this they are laid out as one
unbreakable run and leave the box sideways.

```css
.body { overflow: hidden; overflow-wrap: break-word }
```

`word-break: break-all` is also honoured and breaks more aggressively —
anywhere, rather than only where a word would otherwise overflow.

## Just cut it

```css
.fixed { overflow: hidden }
```

`overflow: hidden` clips in both directions, which is why it is the base of
every recipe above.

## Not supported

| What | Instead |
| ---- | ------- |
| `-webkit-line-clamp`, `display: -webkit-box` | `max-lines` + `continue: discard`. Prefixed properties are ignored with a warning. |
| `text-overflow: ellipsis` on a wrapping block | It needs `white-space: nowrap`. For several lines, use the clamp. |
| `line-clamp` as a shorthand | Write the three longhands; they are what the tests cover. |

## The real defence

Give the box a size, pick a recipe, and let a long value be cut politely rather
than break the layout. [`examples/business-promo`](../../examples/business-promo/)
carries a deliberately over-long blurb for exactly this reason — see its
[`template.html`](../../examples/business-promo/template.html).
