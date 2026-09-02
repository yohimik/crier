# Reddit

Reddit uses two hosts. Tokens come from `www.reddit.com`. Everything else goes to `oauth.reddit.com`. Both are configurable. This lets the end-to-end tests point them at one fake.

## What you need

You need a **script** app. Go to https://www.reddit.com/prefs/apps. Click create app, then choose script.
Keep the client id and the secret. The client id is under the app name.

## Setting it up

```yaml
publish:
  reddit:
    enabled: true
    username: yourbot
    subreddit: yoursubreddit     # without the r/
    title: "{{ .product }} {{ .version }}"
    kind: auto                   # auto, image, video or link
    flair-id: ""                 # required by some subreddits
```

```sh
export CRIER_PUBLISH_REDDIT_CLIENT_ID="…"
export CRIER_PUBLISH_REDDIT_CLIENT_SECRET="…"
export CRIER_PUBLISH_REDDIT_PASSWORD="…"
```

## Two ways to authenticate

**Password grant** (the above) is the simplest method. It has two problems you should know about:

- The token lasts an hour and comes with **no refresh token**. This means crier asks for a new one each run.
- It **fails outright on an account with two-factor authentication**. It returns an `invalid_grant` that explains nothing.

**A refresh token** avoids both problems. Obtain one through a one-time code flow and set `publish.reddit.refresh-token`. Crier prefers it when it is set. The username and password become unnecessary.

## The User-Agent

Reddit's terms require a descriptive User-Agent. Reddit throttles generic agents hard. This looks like an unexplained rate limit. crier builds

```
cli:com.yohimik.crier:v1.2.3 (by /u/yourbot)
```

from the version and `publish.reddit.username`. `publish.reddit.user-agent` overrides this. crier deliberately does not use its own generic agent here.

## How a post is made

An **image** or **video** post takes three steps:

1. Call `POST /api/media/asset.json`. This leases a slot and returns a signed form for Reddit's object store. The form fields arrive as an **array** of name/value pairs. Their order is part of the signature.
2. POST the file to that store. Send the fields first in order. Send the file part **last**.
3. Call `POST /api/submit` with `kind=image` (or `video`) and the resulting URL.

A **video** post also needs a poster image. crier renders frame 0 as a JPEG and uploads it as `video_poster_url`. There is nothing to configure.

Using `kind: link` skips the upload entirely. It submits a [staged](../staging/README.md) URL.

The lease is single use. The upload is never retried on a 5xx. The submit is never retried either.

## The permalink

A media post's submit response usually carries no post id at all. Reddit used to report it over a websocket. That websocket has been unreliable since 2023. As a result, crier polls `/user/<name>/submitted` for the new post's permalink. This is best effort: the post is made either way. A failure here is a warning rather than an error.

## Rate limits

The free tier allows 100 queries per minute per client. The retry logic in crier honours `Retry-After`.

## Check it

```sh
crier ping
```

It posts nothing. Instead, ping gets a token and calls `GET /api/v1/me`. This is where a two-factor account fails. See [the command line](../operations/cli.md#crier-ping).

## Animated GIFs

A GIF is leased with mime type `image/gif`. Submit it as `kind=image` to keep it animated. `videogif` is for an MP4 standing in for a GIF. It wants a poster URL. Crier has no reason to produce this URL.

Set `render.video.format: gif`. See [video](../rendering/video.md).

One file per post. Reddit has galleries, but the only way to make one is an endpoint Reddit documents nowhere and makes no promises about, so a paged run becomes a run of ordinary posts instead. See [pagination and carousels](../rendering/pagination.md).

Configuration keys: [`publish.reddit.*`](../configuration/publish/reddit.md).
