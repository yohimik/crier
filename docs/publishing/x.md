# X

## What you need

A paid API tier. The free tier cannot post media, which makes it useless here.

An OAuth 2.0 user access token with `tweet.write`, `users.read` and
`offline.access`. Obtaining one is a browser flow crier does not implement —
`crier auth` is a follow-up, not a feature.

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

`POST /2/media/upload` as multipart, then `POST /2/tweets` with the media id.
The tweet call is never retried on a 5xx.

## Video

Chunked, because X has no other way:

1. `INIT` with the total size and `media_category=tweet_video`.
2. `APPEND` once per segment of at most 5MB, numbered from zero.
3. `FINALIZE`.
4. Poll `STATUS` until the transcode succeeds, honouring
   `processing_info.check_after_secs` — that field is the difference between
   one poll and a dozen 429s.
5. `POST /2/tweets`.

Limits: 512MB, 140 seconds. crier checks the size before starting.

## Check it

```sh
crier ping
```

Nothing is posted: ping calls `GET /2/users/me`. See [the command line](../operations/cli.md#crier-ping).

## Animated GIFs

A GIF goes up through the chunked upload with `media_category=tweet_gif` — as `tweet_video` it would come out as a silent video. The limit is **15MB**, not the 512MB a video gets, and crier checks it before uploading.

Set `render.video.format: gif` — see [video](../rendering/video.md).

Configuration keys: [`publish.x.*`](../configuration/reference/publish-x.md).
