# TikTok

## What you need

You need a TikTok developer app with the Content Posting API. You also need an access token with `video.publish`. Include `video.upload` for photos.

**The app has to pass audit** before it can post publicly. Until it does, every post is restricted to `SELF_ONLY`. This is why `SELF_ONLY` is crier's default. An unaudited app posting `PUBLIC_TO_EVERYONE` fails. Defaulting to the restrictive value fails safely instead.

## Setting it up

```yaml
publish:
  tiktok:
    enabled: true
    privacy-level: SELF_ONLY   # MUTUAL_FOLLOW_FRIENDS, FOLLOWER_OF_CREATOR, PUBLIC_TO_EVERYONE
    title: "{{ .product }} {{ .version }}"
```

```sh
export CRIER_PUBLISH_TIKTOK_TOKEN="…"
```

## Photos are pulled, video is pushed

A **photo** post uses `PULL_FROM_URL`. TikTok fetches the image, so this platform needs [staging](../staging/README.md).

A **video** is uploaded in chunks. The `PULL_FROM_URL` method for video requires a domain verified with TikTok. Most people setting up crier will not have this. As a result, crier always uses `FILE_UPLOAD`:

- Upload sequential chunks between 5MB and 64MB. Include a `Content-Range` header.
- Combine any remainder smaller than 5MB with the last chunk. This prevents TikTok from rejecting a short chunk.
- Upload any file under 5MB as a single chunk.

Then, poll `status/fetch` until you get `PUBLISH_COMPLETE`.

The limits are 10 minutes and 4GB.

## Errors that arrive as a 200

TikTok answers `200 OK` with an error object inside. crier reads it and reports the code, the message and the log id. This is what their support will ask for.

## Check it

```sh
crier ping
```

Nothing is posted. Ping calls `creator_info/query/`. The posting scope must also allow this call. See [the command line](../operations/cli.md#crier-ping).

## Animated GIFs

**Not supported.** TikTok only supports MP4 for animations. If you enable this platform and set `render.video.format: gif`, you will get a configuration error before rendering starts. Use `render.video.format: mp4` instead. See [video](../rendering/video.md).

A photo post takes up to thirty-five images; it was always a list of URLs, so several pages are more entries in the same request. See [pagination and carousels](../rendering/pagination.md).

Configuration keys: [`publish.tiktok.*`](../configuration/publish/tiktok.md).
