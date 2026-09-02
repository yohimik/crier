# Threads

```yaml
publish:
  threads:
    enabled: true
    user-id: "1234567890123456"

stage:
  mode: s3                   # or server, or url: see the staging docs
```

```sh
export CRIER_PUBLISH_THREADS_TOKEN="…"
crier ping     # is the token right?
crier
```

Threads will not accept an upload. Like Instagram, it takes a **public URL** and fetches the media itself from Meta's own servers. So a Threads post needs [staging](../staging/README.md) configured, and a laptop needs a tunnel.

## The token is not an Instagram token

This is the part that catches people out. Threads is its own API on its own host, with its own OAuth scopes, and an Instagram Graph token is refused here.

1. Open your app at [developers.facebook.com](https://developers.facebook.com/apps), then **Add use case → Threads**.
2. Add the **threads_basic** and **threads_content_publish** permissions. The first reads the account, the second creates posts. Without the second, `crier ping` passes and publishing fails.
3. Run the Threads login flow and exchange the short-lived token for a long-lived one, which lasts sixty days and can be refreshed.
4. Copy it into `CRIER_PUBLISH_THREADS_TOKEN`.

The account has to be a Threads account, not only an Instagram one. Threads has to have been opened for it at least once.

## The user id

`publish.threads.user-id` is the Threads account's own id. It is not the Instagram professional account id, even though the two accounts are linked and the numbers sit next to each other in the dashboard.

Ask for it:

```sh
curl "https://graph.threads.net/v1.0/me?fields=id,username&access_token=$CRIER_PUBLISH_THREADS_TOKEN"
```

`crier ping` asks the same question and reports the answer. If the id it gets back is not the one in the configuration, the row says so, because the wrong id is the common cause of a post that goes nowhere.

## How a post is made

1. `POST /{user-id}/threads` with `media_type=IMAGE` and `image_url`, or `media_type=VIDEO` and `video_url`, plus the caption as `text`. This creates a container.
2. Poll the container until `status` is `FINISHED`. This is Meta fetching the URL. A URL that is not reachable fails here, and the reason arrives in `error_message`.
3. `POST /{user-id}/threads_publish` with `creation_id`. This is the post.

Step 3 is never retried on a 5xx: it may have created the post. The one exception is Meta's own not-ready refusal, error 9007 or error 24 with subcode 2207006, which means the publish did not happen. crier asks again within the poll budget. It is the same race Instagram serves, on the same infrastructure.

If step 2 or 3 fails, the container id is logged at warning level. Unpublished containers expire on their own.

## Several pages, one carousel

Up to twenty pages become one carousel, which is what makes a paginated changelog one entry on the feed rather than twenty. Each page is a child container created with `is_carousel_item=true`, then a parent with `media_type=CAROUSEL` lists them in page order, then the parent is published. The text belongs to the parent.

**A carousel needs at least two items.** Threads refuses a `CAROUSEL` container naming one child, so a single-page run posts as a plain `IMAGE` instead. crier picks between the two on its own; there is nothing to configure.

A page list longer than twenty becomes several posts in a row, in order. See [pagination and carousels](../rendering/pagination.md).

## The caption is the post

Threads has no caption field beside the picture. The `text` parameter **is** the post, and the media hangs off it. So `publish.threads.caption`, or the shared `publish.caption`, becomes the words people read.

Text is capped at 500 characters. crier does not truncate: a caption over the limit is refused by the API, so the words that were written are the words that go out or nothing does.

## Limits worth knowing

| | Limit |
| --- | ----- |
| Posts | 250 per 24 hours, per account |
| Image | 8MB, JPEG or PNG |
| Video | 1GB, up to 5 minutes |
| Text | 500 characters |
| Carousel | 2 to 20 items |

Unlike Instagram, Threads takes PNG as well as JPEG, so a project configured for PNG needs no second encode for it.

## Animated GIFs

**Not supported.** There is no way to post a GIF as a file. Setting `render.video.format: gif` with this platform enabled is a configuration error, raised before anything is rendered, and it names the platform. Use `render.video.format: mp4`. See [video](../rendering/video.md).

## When the fetch fails

`status: ERROR` almost always means Meta could not reach the URL. The causes, in order of likelihood:

- The URL is a `localhost` or private address. See [tunnels](../staging/tunnels.md).
- The URL serves an interstitial page. Meta's fetcher receives the page instead of the image.
- A presigned URL expired between staging and the fetch.
- The URL is a redirect. Meta's fetcher does not follow them.

The reason Meta gave is in the error, because `error_message` is asked for alongside the status.

## Check it

```sh
crier ping
```

Nothing is posted. Ping reads `GET /me?fields=id,username`, which is the one question a Threads token can always be asked: it answers with the account the token was issued for. See [the command line](../operations/cli.md#crier-ping).

## What you get back

The post id and its link. The link comes from `GET /{media-id}?fields=permalink`, because only Threads knows the post's address. If that lookup fails it is logged and the post is still reported as published: the post exists, and no link is better than one that goes nowhere.

## Music

Threads has no way for crier to attach an audio file, and no lead video either: a post is pictures or a video, never both. Setting `publish.threads.music-file` or `publish.threads.lead-video` is a configuration error that says so. See [music](./music.md).

Configuration keys: [`publish.threads.*`](../configuration/publish/threads.md).
