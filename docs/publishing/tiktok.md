# TikTok

## What you need

A TikTok developer app with the Content Posting API, and an access token with
`video.publish` (and `video.upload` for photos).

**The app has to pass audit** before it can post publicly. Until it does, every
post is restricted to `SELF_ONLY`, which is why that is crier's default: an
unaudited app posting `PUBLIC_TO_EVERYONE` fails, and defaulting to the
restrictive value fails safely instead.

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

A **photo** post uses `PULL_FROM_URL`: TikTok fetches the image, so this
platform needs [staging](../staging/README.md).

A **video** is uploaded in chunks. `PULL_FROM_URL` for video requires a domain
verified with TikTok, which most people setting crier up will not have, so
crier always uses `FILE_UPLOAD`:

- chunks between 5MB and 64MB, sequential, with a `Content-Range` header;
- a remainder smaller than 5MB rides along with the last chunk rather than
  becoming a short one TikTok would reject;
- a file under 5MB goes as a single chunk.

Then `status/fetch` is polled until `PUBLISH_COMPLETE`.

Limits: 10 minutes, 4GB.

## Errors that arrive as a 200

TikTok answers `200 OK` with an error object inside. crier reads it and reports
the code, the message and the log id — which is what their support will ask
for.

## Check it

```sh
crier ping
```

Nothing is posted: ping calls `creator_info/query/`, which is also the call the posting scope has to allow. See [the command line](../operations/cli.md#crier-ping).

## Animated GIFs

**Not supported.** TikTok has no animation path that does not mean uploading an
MP4, so `render.video.format: gif` with this platform enabled is a
configuration error named before anything is rendered. Use
`render.video.format: mp4` — see [video](../rendering/video.md).

Configuration keys: [`publish.tiktok.*`](../configuration/reference/publish-tiktok.md).
