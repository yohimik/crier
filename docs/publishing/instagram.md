# Instagram

Instagram is the strictest platform crier talks to. It is the reason [staging](../staging/README.md) exists.

Instagram will not accept an upload. It takes a **public URL** and fetches the media itself from Meta's own servers. This means the machine running crier has to be reachable from the internet, or the file has to be somewhere that is.

## What you need

- An Instagram **professional** account (Business or Creator).
- A Facebook Page linked to it.
- A Meta app with the Instagram Graph API. It must be reviewed for `instagram_content_publish`.
- A long-lived access token.

You cannot skip the app review for publishing.

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

The user id is the Instagram **professional account** id. It is not the Facebook Page id.

## JPEG only

Instagram rejects a PNG `image_url`. crier declares this in the publisher's needs. As a result, a project configured for PNG gets a JPEG encoded as well. This is for Instagram alone. You do not need to configure anything.

## How a post is made

1. Send a `POST /{user-id}/media` request with the `image_url` (or `video_url`) and the caption. This creates a container.
2. Poll the container until `status_code` is `FINISHED`. This is Meta fetching the URL. A URL that is not reachable fails here.
3. Send a `POST /{user-id}/media_publish` request with the container id.

Step 3 is never retried on a 5xx. It may have created the post.

If step 2 or 3 fails, the container id is logged at warning level. It expires in 24 hours by itself.

## Stories and reels

| `story` | Image | Video |
| --- | --- | --- |
| `false` | feed post | `REELS` |
| `true` | `STORIES` | `STORIES` |

Reels are 3 seconds to 15 minutes. Stories are up to 60.

**Stories carry no caption.** The API has no field for one. Meta ignores the parameter. Because of this, crier does not send it. It warns you when a caption was configured.
Text that must appear on a story belongs in the image itself. Bake it into the template, or give the story pass an [overlay](../templates/overlays.md) of its own.

## When the fetch fails

`status_code: ERROR` almost always means Meta could not reach the URL. Here are the causes in order of likelihood:

- The URL is a `localhost` or private address. See [tunnels](../staging/tunnels.md).
- The URL serves an ngrok free-tier interstitial page. Meta's fetcher receives this page instead of the image.
- You used a presigned URL that expired between staging and the fetch.
- The URL is a redirect. Meta's fetcher does not follow them.

## Check it

```sh
crier ping
```

This command does not post anything. Instead, ping reads the account using `GET /{user-id}?fields=id,username`. See [the command line](../operations/cli.md#crier-ping).

## Animated GIFs

**Not supported.** Instagram only supports animations as MP4 uploads. Setting `render.video.format: gif` with this platform enabled is a configuration error. This error is raised before anything is rendered. Use `render.video.format: mp4`. See [video](../rendering/video.md).

## What you get back

You get the media id and the post's link. The link is fetched with `GET {media-id}?fields=permalink`. The extra request exists because the media id is not the shortcode. Using `instagram.com/p/<media-id>` results in a 404.

If the permalink lookup fails, it is logged. The post is still reported as published. The post exists, and no link is better than one that goes nowhere.

## Stories are 1080×1920

Instagram crops whatever it is given to the story shape on its own servers. It does not say where it makes the cut. The `fit: cover` setting does the cropping here instead:

```yaml
publish:
  instagram:
    story: true
    width: 1080
    height: 1920
    fit: cover
```

The card is drawn at `render.width` × `render.height`. It is then resampled into the story frame. This means the approved design is exactly what goes out. The middle is kept and the edges are lost. This is done visibly and on purpose. See [fitting the platform](../templates/overlays.md#fitting-the-platform).

A feed post takes up to ten images as one carousel. A story takes one, because the Stories API has no carousel: a paged run posts one story per page, in order, each live before the next is created. See [pagination and carousels](../rendering/pagination.md).

Configuration keys: [`publish.instagram.*`](../configuration/publish/instagram.md).
