# LinkedIn

## What you need

An OAuth 2.0 access token with `w_member_social` (a person) or
`w_organization_social` (a company page), and the author URN.

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

`publish.linkedin.version` is the mandatory `LinkedIn-Version` header, a
`YYYYMM` string. LinkedIn retires versions; when calls start failing with 426,
this is the value to bump.

crier sends `X-Restli-Protocol-Version: 2.0.0` on every call. Omitting either
header gets a 426 that says nothing useful, which is why they are set in one
place.

## Images

1. `POST /rest/images?action=initializeUpload` with the owner URN.
2. `PUT` the bytes to the URL that comes back.
3. `POST /rest/posts` referring to the image URN.

## Video

Longer, and the ETags matter:

1. `POST /rest/videos?action=initializeUpload` with the owner and the file size.
2. `PUT` each 4MiB byte-range part, **keeping every response's ETag**.
3. `POST /rest/videos?action=finalizeUpload` with the ETags **in part order**.
4. Poll the video until its status is `AVAILABLE`.
5. `POST /rest/posts`.

A missing or reordered ETag produces a video that uploads and then never
becomes available, so crier refuses a part that came back without one.

MP4 only, 3 seconds to 30 minutes.

## What you get back

The post URN, from the `x-restli-id` response header.

Configuration keys: [`publish.linkedin.*`](../configuration/reference/publish-linkedin.md).
