# Instagram

The strictest platform crier talks to, and the reason
[staging](../staging/README.md) exists.

Instagram will not accept an upload. It takes a **public URL** and fetches the
media itself, from Meta's own servers — so the machine running crier has to be
reachable from the internet, or the file has to be somewhere that is.

## What you need

- An Instagram **professional** account (Business or Creator).
- A Facebook Page linked to it.
- A Meta app with the Instagram Graph API, reviewed for
  `instagram_content_publish`.
- A long-lived access token.

There is no way around the app review for publishing.

## Setting it up

```yaml
publish:
  instagram:
    enabled: true
    user-id: "17841400000000000"
    story: false               # true posts a story instead of a feed post
    api-base-url: https://graph.facebook.com/v25.0

stage:
  mode: s3                     # or server, or url — see the staging docs
```

```sh
export CRIER_PUBLISH_INSTAGRAM_TOKEN="…"
```

The user id is the Instagram **professional account** id, not the Facebook Page
id.

## JPEG only

Instagram rejects a PNG `image_url`. crier declares this in the publisher's
needs, so a project configured for PNG gets a JPEG encoded as well, for
Instagram alone. Nothing to configure.

## How a post is made

1. `POST /{user-id}/media` with `image_url` (or `video_url`) and the caption,
   creating a container.
2. Poll the container until `status_code` is `FINISHED` — this is Meta
   fetching the URL, and where a URL that is not reachable fails.
3. `POST /{user-id}/media_publish` with the container id.

Step 3 is never retried on a 5xx: it may have created the post.

If step 2 or 3 fails, the container id is logged at warning level. It expires
in 24 hours by itself.

## Stories and reels

| `story` | Image | Video |
| --- | --- | --- |
| `false` | feed post | `REELS` |
| `true` | `STORIES` | `STORIES` |

Reels are 3 seconds to 15 minutes; stories up to 60.

## When the fetch fails

`status_code: ERROR` almost always means Meta could not reach the URL. In
order of likelihood:

- a `localhost` or private address — see [tunnels](../staging/tunnels.md);
- an ngrok free-tier interstitial page, which Meta's fetcher receives instead
  of the image;
- a presigned URL that expired between staging and the fetch;
- a redirect: Meta's fetcher does not follow them.

## Check it

```sh
crier ping
```

Nothing is posted: ping reads the account with `GET /{user-id}?fields=id,username`. See [the command line](../operations/cli.md#crier-ping).

## Animated GIFs

**Not supported.** Instagram has no animation path that does not mean uploading an
MP4, so `render.video.format: gif` with this platform enabled is a
configuration error named before anything is rendered. Use
`render.video.format: mp4` — see [video](../rendering/video.md).

## What you get back

The media id, and the post's link — fetched with `GET {media-id}?fields=permalink`,
because the media id is not the shortcode and `instagram.com/p/<media-id>` is a
404. A permalink lookup that fails is logged and the post is still reported as
published: the post exists, and no link is better than one that goes nowhere.

## Stories are 1080×1920

Instagram crops whatever it is given to the story shape on its own servers, and
does not say where it cut. `fit: cover` does the cropping here instead:

```yaml
publish:
  instagram:
    story: true
    width: 1080
    height: 1920
    fit: cover
```

The card is drawn at `render.width` × `render.height` and resampled into the
story frame, so the design everyone approved is the design that goes out — with
the middle kept and the edges lost, visibly and on purpose. See
[fitting the platform](../templates/overlays.md#fitting-the-platform).

Configuration keys: [`publish.instagram.*`](../configuration/reference/publish-instagram.md).
