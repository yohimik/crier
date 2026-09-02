# Mastodon

Mastodon has no central API. The instance's own base URL is the endpoint. The token belongs to that instance alone.

## Setting it up

1. Go to your instance. Navigate to Preferences, then Development, and click New application.
2. Select the `write:media` and `write:statuses` scopes.
3. Copy the access token.

```yaml
publish:
  mastodon:
    enabled: true
    api-base-url: https://mastodon.social
    visibility: public          # or unlisted, private, direct
    alt-text: "A release card for {{ .product }} {{ .version }}"
```

```sh
export CRIER_PUBLISH_MASTODON_TOKEN="…"
```

## Alt text

`publish.mastodon.alt-text` becomes the attachment's description. Without it, crier uses the caption. It is a [template](../templates/captions.md) like every other text key. Writing one is the difference between a post a screen reader can read and one it cannot.

## Processing

A video comes back as `202 Accepted`. This means it is "not ready". Posting a status that refers to an attachment in that state is rejected. Because of this, crier polls `/api/v1/media/:id` until it returns 200. The `poll-interval` and `poll-timeout` settings control how patiently it waits.

## Duplicates

crier sends an `Idempotency-Key`. It derives this key from the artifact and the caption. This ensures an instance collapses a repeat rather than posting twice.

## Check it

```sh
crier ping
```

Nothing is posted. A ping calls `GET /api/v1/accounts/verify_credentials`. See [the command line](../operations/cli.md#crier-ping).

## Animated GIFs

A GIF uses the same v2 media endpoint as a video. This includes the 202-and-poll wait while the instance processes it.

Set `render.video.format: gif`. See [video](../rendering/video.md).

Configuration keys: [`publish.mastodon.*`](../configuration/reference/publish-mastodon.md).
