# Reddit

Reddit is two hosts: tokens come from `www.reddit.com` and everything else goes
to `oauth.reddit.com`. Both are configurable, which is what lets the end-to-end
tests point them at one fake.

## What you need

A **script** app: https://www.reddit.com/prefs/apps → create app → script.
Keep the client id (under the app name) and the secret.

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

**Password grant** (the above) is the simplest, and it has two problems worth
knowing before you meet them:

- the token lasts an hour and comes with **no refresh token**, so crier asks
  for a new one each run;
- it **fails outright on an account with two-factor authentication**, with an
  `invalid_grant` that explains nothing.

**A refresh token** avoids both. Obtain one through a one-time code flow and
set `publish.reddit.refresh-token`; crier prefers it when it is set, and the
username and password become unnecessary.

## The User-Agent

Reddit's terms require a descriptive one, and a generic agent is throttled hard
in a way that looks like an unexplained rate limit. crier builds

```
cli:com.yohimik.crier:v1.2.3 (by /u/yourbot)
```

from the version and `publish.reddit.username`. `publish.reddit.user-agent`
overrides it. crier's own generic agent is deliberately not used here.

## How a post is made

An **image** or **video** post is three steps:

1. `POST /api/media/asset.json` leases a slot and returns a signed form for
   Reddit's object store. The form's fields arrive as an **array** of
   name/value pairs, and their order is part of the signature.
2. The file is POSTed to that store with the fields first, in order, and the
   file part **last**.
3. `POST /api/submit` with `kind=image` (or `video`) and the resulting URL.

A **video** post also needs a poster image. crier renders frame 0 as a JPEG and
uploads it as `video_poster_url` — nothing to configure.

`kind: link` skips the upload entirely and submits a
[staged](../staging/README.md) URL.

The lease is single use, so the upload is never retried on a 5xx; neither is
the submit.

## The permalink

A media post's submit response usually carries no post id at all. Reddit used
to report it over a websocket, which has been unreliable since 2023, so crier
polls `/user/<name>/submitted` for the new post's permalink. It is best effort:
the post is made either way, and a failure here is a warning rather than an
error.

## Rate limits

The free tier is 100 queries per minute per client. crier's retry honours
`Retry-After`.

## Check it

```sh
crier ping
```

Nothing is posted: ping gets a token and calls `GET /api/v1/me`, which is where a two-factor account fails. See [the command line](../operations/cli.md#crier-ping).

## Animated GIFs

A GIF is leased with mime type `image/gif` and submitted as `kind=image`, which keeps it animated. `videogif` is for an MP4 standing in for a GIF and wants a poster URL crier has no reason to produce.

Set `render.video.format: gif` — see [video](../rendering/video.md).

Configuration keys: [`publish.reddit.*`](../configuration/reference/publish-reddit.md).
