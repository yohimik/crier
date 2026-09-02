# Facebook

This posts to a Page. Unlike Instagram, it accepts an upload. It works with no staging at all.

## What you need

- A Facebook Page.
- A Meta app with `pages_manage_posts` and `pages_read_engagement`.
- A **Page** access token. Do not use a user token.

## Setting it up

```yaml
publish:
  facebook:
    enabled: true
    page-id: "123456789012345"
    story: false      # true posts a Page story
    use-url: false    # true sends the staged URL instead of the bytes
```

```sh
export CRIER_PUBLISH_FACEBOOK_TOKEN="…"
```

## Photos, and stories

A photo post requires a single call to `POST /{page-id}/photos`. You send the file as `source` and the caption as `message`.

A story requires two calls. First, upload the photo with `published=false`. You will receive an id. Post this id to `/{page-id}/photo_stories`. Only the second call is irreversible. This means only the second call skips retries.

## `use-url`

With `publish.facebook.use-url`, crier sends the staged URL instead of the bytes. This is useful when the file is already on a CDN and re-uploading it is a waste. This setting makes the platform need staging. Otherwise, it does not.

## Video

You can upload a video using `POST /{page-id}/videos`. Pass the video in the `source` or `file_url` field. Send the caption as the `description`. The video can be up to 1GB and 20 minutes long.

## Check it

```sh
crier ping
```

It posts nothing. The ping command reads the Page with `GET /{page-id}?fields=id,name`. See [the command line](../operations/cli.md#crier-ping).

## Animated GIFs

**Not supported.** Facebook only supports MP4 files for animations. Setting `render.video.format: gif` with this platform enabled causes a configuration error before anything is rendered. Use `render.video.format: mp4`. See [video](../rendering/video.md).

Configuration keys: [`publish.facebook.*`](../configuration/reference/publish-facebook.md).
