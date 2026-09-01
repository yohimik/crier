# Mastodon

There is no central API: the instance's own base URL is the endpoint, and the
token belongs to that instance alone.

## Setting it up

1. On your instance: Preferences → Development → New application.
2. Scopes: `write:media` and `write:statuses`.
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

`publish.mastodon.alt-text` becomes the attachment's description. Without it
crier uses the caption. It is a [template](../templates/captions.md) like every
other text key, and writing one is the difference between a post a screen
reader can read and one it cannot.

## Processing

A video comes back as `202 Accepted`, meaning "not ready". Posting a status
that refers to an attachment in that state is rejected, so crier polls
`/api/v1/media/:id` until it returns 200 — `poll-interval` and `poll-timeout`
control how patiently.

## Duplicates

crier sends an `Idempotency-Key` derived from the artifact and the caption, so
an instance collapses a repeat rather than posting twice.

## Check it

```sh
crier ping
```

Nothing is posted: ping calls `GET /api/v1/accounts/verify_credentials`. See [the command line](../operations/cli.md#crier-ping).

Configuration keys: [`publish.mastodon.*`](../configuration/reference/publish-mastodon.md).
