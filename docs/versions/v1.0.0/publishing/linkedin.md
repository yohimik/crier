# LinkedIn

## What you need

You need an OAuth 2.0 access token and the author URN. The token must include `w_member_social` for a person or `w_organization_social` for a company page.

## Setting it up

```yaml
publish:
  linkedin:
    enabled: true
    author-urn: "urn:li:person:AbC123"      # or urn:li:organization:12345678
    version: "202606"
```

```sh
export CRIER_PUBLISH_LINKEDIN_TOKEN="…"
```

`publish.linkedin.version` is the mandatory `LinkedIn-Version` header. It requires a `YYYYMM` string. LinkedIn retires old versions. When calls start failing with a 426 error, bump this value.

crier sends `X-Restli-Protocol-Version: 2.0.0` on every call. If you omit either header, you get a 426 error that says nothing useful. This is why they are set in one place.

## Images

1. Send a `POST /rest/images?action=initializeUpload` request. Include the owner URN.
2. `PUT` the bytes to the URL that comes back.
3. Send a `POST /rest/posts` request. Refer to the image URN.

## Video

Video uploads take longer. You must also track ETags carefully:

1. `POST /rest/videos?action=initializeUpload` with the owner and the file size.
2. `PUT` each 4MiB byte-range part, **keeping every response's ETag**.
3. `POST /rest/videos?action=finalizeUpload` with the ETags **in part order**.
4. Poll the video until its status is `AVAILABLE`.
5. `POST /rest/posts`.

If an ETag is missing or out of order, the video uploads but never becomes available. This is why crier refuses any part that comes back without an ETag.

Files must be MP4 only. The duration must be 3 seconds to 30 minutes.

## What you get back

You get the post URN. It comes from the `x-restli-id` response header.

## Check it

```sh
crier ping
```

Nothing is posted: ping calls `GET /v2/userinfo`. See [the caveat](../operations/cli.md#linkedin-is-a-special-case) because a posting-only token cannot read a profile. See [the command line](../operations/cli.md#crier-ping).

## Animated GIFs

**Not supported.** LinkedIn requires an MP4 upload for animations. Setting `render.video.format: gif` with this platform enabled is a configuration error. This error is caught before anything is rendered. Use `render.video.format: mp4` instead. See [video](../rendering/video.md).

Two to twenty images go as one multi-image post. One image is a different request shape entirely, so crier picks the shape from how many there are. See [pagination and carousels](../rendering/pagination.md).

Configuration keys: [`publish.linkedin.*`](../configuration/publish/linkedin.md).
