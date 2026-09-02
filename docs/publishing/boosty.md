# Boosty

> **Boosty publishes no API.** There is no developer console, no documentation, no support channel and no promise. What crier talks to is the private REST service the boosty.to web app calls, reverse engineered by the community and bound by several open clients. It can change tomorrow, without notice and without a version number.
>
> crier deals with that in three ways. It pins what it expects in a test fake, so a change shows up as a failing test rather than as a broken post. It keeps both hosts in configuration (`publish.boosty.api-base-url`, `publish.boosty.upload-base-url`), so an address that moves can be pointed somewhere else without a new release. And it never guesses: video and audio are left out because nothing pins their endpoints, rather than shipped as a hopeful call.
>
> Use it knowing that. If Boosty ever ships a real API, this page changes.

```yaml
publish:
  boosty:
    enabled: true
    blog: your-blog-slug
    access: free
```

```sh
export CRIER_PUBLISH_BOOSTY_ACCESS_TOKEN="…"
crier ping     # is the token right, and can it post there?
crier
```

Boosty takes the bytes. It does not need [staging](../staging/README.md).

## What was pinned, and from where

Nothing here is invented. Each piece comes from a maintained client or from captured editor traffic:

| Piece | Where it came from |
| ----- | ------------------ |
| `POST /oauth/token/` with `device_id`, `device_os=web`, `grant_type=refresh_token`, `refresh_token` | [barsikus007/boosty](https://github.com/barsikus007/boosty), [ath31st/boosty_api_rs](https://github.com/ath31st/boosty_api_rs), [HOCKI1/py_boosty_api](https://github.com/HOCKI1/py_boosty_api) — all three agree |
| `POST /v1/blog/{blog}/post/`, form encoded, with `data` as a JSON block array | barsikus007/boosty and HOCKI1/py_boosty_api |
| `price` and `subscription_level_id` as the access level | HOCKI1/py_boosty_api, and the post model in barsikus007/boosty |
| `GET /v1/blog/{blog}` for the blog | [akovardin/boosty](https://github.com/akovardin/boosty), which types the whole response |
| The upload host, `POST /image`, the numbered parts and `/complete` | [k0te1ch/PodBoxBot](https://github.com/k0te1ch/PodBoxBot), whose client documents the flow as captured from the editor's own traffic |

Where clients disagreed, the more recently maintained one won. They did not disagree on anything crier uses.

## Getting the tokens

There is no app to register. The credentials are a signed-in browser session's, which is also why they expire.

1. Sign in at [boosty.to](https://boosty.to) in a normal browser window.
2. Open the developer tools, go to **Application → Local Storage → https://boosty.to**.
3. The `auth` key holds a JSON object. Copy `accessToken` and `refreshToken` out of it.
4. The `_clientId` key holds a UUID. That is the device id.

```sh
export CRIER_PUBLISH_BOOSTY_ACCESS_TOKEN="…"     # auth.accessToken
export CRIER_PUBLISH_BOOSTY_REFRESH_TOKEN="…"    # auth.refreshToken
export CRIER_PUBLISH_BOOSTY_DEVICE_ID="…"        # _clientId
```

`publish.boosty.blog` is the slug in your own URL. For `boosty.to/crierhq` it is `crierhq`.

## The tokens expire, and the refresh token is spent when it is used

An access token on its own works until it runs out. Then every call comes back 401 and the run fails.

Set the refresh token and the device id and crier renews it for you. On the first 401 it calls the refresh endpoint once, then repeats the call that failed. Once per run: a token that is simply wrong is traded once, not on every request.

Boosty rotates the refresh token. The one crier sends is spent, and the answer carries a replacement. crier has nowhere to write that, because the token is your secret rather than crier's file, so it goes to the log as a warning:

```
WRN boosty issued a new refresh token and spent the old one; update
    publish.boosty.refresh-token or the next run cannot renew
    refresh-token=… expires_in=3600
```

Copy that value into your secret store. The next run cannot renew without it.

Without the pair, a 401 is reported with the reason:

```
WRN boosty refused the access token and there is nothing to refresh it with;
    a boosty access token expires, so set publish.boosty.refresh-token and
    publish.boosty.device-id to have crier renew it
```

Both halves or neither. A refresh token with no device id is refused at startup, because a refresh that could never run is not something to find out about hours later.

## Who can read the post

`publish.boosty.access` is one key. On the wire it is two numbers on the same call, which is why the missing companion key is a startup error rather than a surprise on the feed.

| `access` | Extra key | `price` | `subscription_level_id` | Who sees it |
| -------- | --------- | ------- | ----------------------- | ----------- |
| `free` (default) | — | `0` | `0` | everybody |
| `paid` | `price` | the price | `0` | anybody who buys the post once |
| `level` | `level-id` | `0` | the level id | subscribers at that tier and above |

`access: paid` with no price would be a free post. So would `access: level` with no level id. Both are refused before anything is uploaded:

```
crier: publish.boosty.price: invalid value "0": want more than 0 when publish.boosty.access is paid
```

**Finding a level id.** Open your blog's **Subscription levels** page while signed in and watch the network tab: `GET /v1/blog/{blog}/subscription_level/` answers with your tiers, each carrying an `id`. That number is `publish.boosty.level-id`.

**The price is in a currency.** `publish.boosty.currency` says which, and travels as the `X-Currency` header the web app sends. It defaults to `RUB`. Set it to whatever your blog is priced in, because the number means nothing without it.

## How a post is made

Pictures go to a different host from the post. `publish.boosty.upload-base-url` is `https://upload.boosty.to` and the same bearer token reaches it.

Each picture takes three calls:

1. `POST /image` with an empty JSON object answers with a `fileId`.
2. `POST /upload/{fileId}` carries the bytes as `application/octet-stream`, in parts of at most 5MB, each with an `X-PartNumber` header counting from one. A part without that header is refused, so the number is not decoration.
3. `POST /upload/{fileId}/complete` finishes the file.

Then one call to the API host makes the post. `POST /v1/blog/{blog}/post/` is form encoded, and its `data` field is a JSON array of content blocks:

```json
[
  {"type":"text","content":"[\"crier v1.2.3 is out\",\"unstyled\",[]]","modificator":""},
  {"type":"text","content":"","modificator":"BLOCK_END"},
  {"type":"image","id":"…","uploadId":"…","url":"https://images.boosty.to/image/…","rendition":"","size":40213,"data":{}}
]
```

The nesting is not a mistake. A text block's `content` is itself a JSON string holding the paragraph, its style and its entities, which is the editor's own serialisation. Each paragraph is closed by a second block carrying the `BLOCK_END` marker.

That create call is the post itself, so crier does not retry it. Nor does it retry the completion call. A 5xx from either may mean the work was done and the answer was lost. The parts are retried, because a part is addressed by its file id and its number, so sending it again replaces the same part.

## Title and caption

The caption becomes the post's text, one block per paragraph. Blank lines are dropped.

The title falls back through a chain:

1. `publish.boosty.title`, which is [templated](../templates/captions.md) like every other text key.
2. The caption's first line.
3. `crier`.

Boosty shows the title above the post and an untitled post reads as a mistake, so the last step is a name rather than a blank.

## Several pages

Up to ten pages go in one post, as ten image blocks in the order they were rendered. That is what makes a paginated changelog one entry on the blog rather than ten.

Ten is crier's own ceiling, not Boosty's. Boosty documents no limit and no client names one, so crier uses the number every other album-shaped platform here settled on. Lower it with `publish.boosty.max-attachments`. A longer page list becomes several posts in a row, in order. See [pagination and carousels](../rendering/pagination.md).

## Pictures only

crier posts images to Boosty and nothing else.

The upload host has an endpoint per media kind. The image one and the audio one are pinned by working clients. The video one is not, and a clip pushed through a guessed endpoint would sit in a post as a file nobody can play, after ffmpeg spent a minute encoding it. Animated GIFs are out for the same reason: nothing says a GIF survives the image pipeline as an animation rather than as a still.

So a run with `render.video.enabled` and `boosty` on is refused before anything is rendered, naming the platform. Video is a follow-up, and it needs somebody to capture the editor uploading one.

`publish.boosty.music-file` is refused too. See [music](./music.md).

## Check it

```sh
crier ping
```

Nothing is posted. crier reads `GET /v1/blog/{blog}`, which is the cheapest question this API answers about a token and a blog at once, and the row names the blog a post would land in.

It also reports the access level, because a paid post that was meant to be free is not something the report would otherwise show:

```
PLATFORM  STATUS  ACCOUNT              NOTE
boosty    ok      Crier HQ (crierhq)   posting behind a one-time price of 300 RUB
```

And it names the case a credential check would otherwise pass: a token that can read the blog and cannot write to it.

```
boosty    ok      Crier HQ (crierhq)   the token reads this blog and cannot post to it;
                                       it belongs to somebody who is not the author
```

Configuration keys: [`publish.boosty.*`](../configuration/publish/boosty.md).
