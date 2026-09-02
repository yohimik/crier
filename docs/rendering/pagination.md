# Pagination and carousels

A card is a fixed size. Content is not. A changelog with four entries fits; the same card with fourteen does not.

crier lets it flow. Content that does not fit becomes the next page, and the pages become a carousel, an album, or a run of posts, depending on the platform. Nothing is configured to make this happen. A template that overflows paginates.

Every recipe here is verified against the renderer by a test in `internal/render/paged_test.go`.

## The shape of a paginated template

Two rules. Do not give the body a fixed height, and let the content flow.

```css
/* This clips. Whatever does not fit is thrown away. */
body { height: 100% }

/* This flows. Whatever does not fit is the next page. */
body { margin: 0 }
```

A fixed height is the usual reason a template that should paginate does not.

## A cover page

Give the first block the height of one page and tell it to break after itself. Everything that follows starts on page two.

```css
@page { margin: 56px 60px 78px }

.cover {
  position: relative;
  height: 946px;          /* 1080 less the page margins */
  break-after: page;
}
```

`position: relative` on the cover lets an element inside it pin to the bottom edge with `position: absolute; bottom: 0`, whatever the content above it does.

## A running header and footer

These live in the page margin, in [margin boxes](https://www.w3.org/TR/css-page-3/#margin-boxes). They repeat on every page without being part of the flow, so they cannot be pushed around by the content.

```css
@page {
  margin: 56px 60px 78px;
  @top-left    { content: "v1.4.0" }
  @bottom-left { content: "example.com" }
  @bottom-right { content: counter(page) " / " counter(pages) }
}
```

`counter(page)` is the page being drawn. `counter(pages)` is how many there are in all. Both resolve after the layout, so `3 / 7` is correct on every page.

A margin box needs a margin to draw in. crier sets the page size and leaves the margin to the template, defaulting to zero, so a template that says nothing gets an edge-to-edge page and a template that asks for a margin gets one. A page rule that sets `margin: 0` and margin boxes at the same time draws them clipped to nothing.

### A different first page

`@page :first` selects the cover. It is how a cover skips a header it does not need.

```css
@page :first {
  @top-left { content: none }
  @bottom-right { content: none }
}
```

Suppressing the counter on page one is worth doing: a single-page render would otherwise announce itself as `1 / 1`.

### A header that follows the content

`string-set` copies text out of the flow, and `string()` reads it back in a margin box. The header then says which section the page belongs to, without the template repeating itself.

```css
h2 { string-set: section content() }
@page { @top-left { content: string(section) } }
```

## Keeping things whole

`break-inside: avoid` stops a box being cut by a page edge. `break-after: avoid` on a heading keeps it with what follows, so a heading is never left alone at the foot of a page.

```css
.card { break-inside: avoid }
.section h2 { break-after: avoid }
```

A box taller than a page is broken anyway. There is nowhere else for it to go.

## The page ceiling

`render.pages-max` is how many pages a document may lay out into. The default is 10 and the hard ceiling is 20.

Past it the render fails and nothing is posted:

```
crier: the document laid out into 14 pages and render.pages-max is 10;
shorten the content, raise render.pages-max (up to 20), or raise render.height
```

The check runs after the layout, because the page count is not knowable until the content has flowed. It exists so a template with a loop that never ends fails here rather than at the platform, which would take the first ten and say nothing about the rest.

## What a platform does with the pages

One run produces one ordered page list. Every platform receives it whole, in that order. The only thing a platform may do to the list is cut it into the sizes it accepts. Nothing reorders, skips, merges or drops pages.

| Platform | Files in one post | Where the number comes from |
| -------- | ----------------- | --------------------------- |
| Instagram (feed) | 10 | a carousel holds ten |
| Instagram (story) | 1 | stories have no carousel |
| Facebook (page) | 10 | crier's ceiling; Meta documents no limit |
| Facebook (story) | 1 | stories have no carousel |
| Telegram | 10 | `sendMediaGroup` takes two to ten |
| X | 4 | `media_ids` takes four |
| Mastodon | 4 | the instance's `max_media_attachments`, four by default |
| Discord | 10 | crier's ceiling; Discord documents no limit |
| LinkedIn | 20 | a multi-image post takes two to twenty |
| Reddit | 1 | see below |
| Slack | 10 | crier's ceiling; Slack documents no limit |
| Custom | `max-attachments` | the command is the platform |

A list longer than the cap becomes several posts in a row rather than a truncated one. Twelve pages at Telegram are a group of ten and a group of two, in that order.

`publish.<platform>.max-attachments` lowers a platform's cap, and only lowers it. Asking a platform for more than it takes would only be refused by the platform, which is a worse way to find out.

### Posts go out one at a time

A platform's posts are published in order, each finished before the next begins. This is not caution. Several platforms order a feed by when a post completed, so publishing two parts of one sequence at once is how they arrive back to front.

A post that fails stops the ones after it, and the report says how far it got:

```
crier: instagram: posts 1 to 3 of 5 went out; post 4 failed and the rest were not sent: ...
```

A gap in the middle of a sequence is worse than a short sequence, because a reader cannot tell it happened.

Platforms still run alongside each other. The sequencing is within one platform, not across them.

### Reddit

Reddit has galleries. The only way to make one is `api/submit_gallery_post`, an endpoint Reddit's own web client uses and Reddit documents nowhere, with no published limit and no promise it keeps working. crier posts one file at a time there instead. Several ordinary posts that will still be there next month beat one gallery built on that.

## Numbering the posts

A caption is rendered once per post, with four values bound:

| Variable | What it is |
| -------- | ---------- |
| `{{ .Post }}` | which post this is |
| `{{ .Posts }}` | how many posts there are |
| `{{ .Page }}` | the first page this post carries |
| `{{ .Pages }}` | how many pages the run produced |

```yaml
publish:
  caption: "Release {{ .version }} ({{ .Post }}/{{ .Posts }})"
```

They read `1 of 1` when nothing paginated, so a caption that mentions them is safe to write either way.

## Rendering the pages to disk

`crier render` writes every page, numbered when there is more than one.

```sh
$ crier render --render-output card.png
card-1.png
card-2.png
card-3.png
```

A document that still fits on one page writes `card.png`, with no number. Nothing that reads a rendered file by name has to learn about pagination to keep working.

## A worked example

The card crier posts about its own releases is a paginated one. It is in [`announce/`](../../announce/): a cover page with the version badge and the install commands, then the changelog flowing across the pages after it, under a running header and a numbered footer. See [the release process](../operations/release.md) for how it is driven.
