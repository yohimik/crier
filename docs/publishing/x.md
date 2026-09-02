# X

## What you need

You need a paid API tier. The free tier cannot post media, which makes it useless here.

You also need an OAuth 2.0 user access token with `tweet.write`, `users.read` and `offline.access`. Getting one requires a browser flow. The crier tool does not implement this flow. The `crier auth` command is a follow-up, not a feature.

## Setting it up

```yaml
publish:
  x:
    enabled: true
    caption: "{{ .product }} {{ .version }} is out"
```

```sh
export CRIER_PUBLISH_X_TOKEN="…"
```

## Images

First, make a multipart request to `POST /2/media/upload`. Then, call `POST /2/tweets` with the media id. The tweet call is never retried on a 5xx.

## Video

You must upload video in chunks because X has no other way:

1. Call `INIT` with the total size and `media_category=tweet_video`.
2. Call `APPEND` once per segment of at most 5MB. Number them from zero.
3. Call `FINALIZE`.
4. Poll `STATUS` until the transcode succeeds. Honour `processing_info.check_after_secs`. That field is the difference between one poll and a dozen 429s.
5. Call `POST /2/tweets`.

The limits are 512MB and 140 seconds. crier checks the size before starting.

## Check it

```sh
crier ping
```

This does not post anything. A ping just calls `GET /2/users/me`. See [the command line](../operations/cli.md#crier-ping).

## Animated GIFs

Upload a GIF through the chunked upload with `media_category=tweet_gif`. If you use `tweet_video`, it comes out as a silent video. The limit is **15MB**, not the 512MB a video gets. Crier checks this limit before uploading.

Set `render.video.format: gif`. See [video](../rendering/video.md).

Configuration keys: [`publish.x.*`](../configuration/reference/publish-x.md).
