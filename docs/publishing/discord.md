# Discord

An incoming webhook. The URL **is** the credential — anyone holding it can post
to that channel — which is why it is the secret rather than a token beside one.

## Setting it up

1. Channel → Edit Channel → Integrations → Webhooks → New Webhook.
2. Copy the URL.

```sh
export CRIER_PUBLISH_DISCORD_WEBHOOK_URL="https://discord.com/api/webhooks/…"
```

```yaml
publish:
  discord:
    enabled: true
    username: "release bot"    # overrides the webhook's display name
```

## Limits

10MB per attachment on a free server; a boosted server allows more. crier warns
at the free-tier limit rather than treating it as absolute.

## Mentions

A caption is posted as the message content, so role and user mentions work:

```yaml
publish:
  discord:
    caption: "{{ .product }} {{ .version }} is out <@&123456789012345678>"
```

## What you get back

The message id and its channel, because crier appends `?wait=true` — without it
Discord answers 204 and says nothing.

## Check it

```sh
crier ping
```

Nothing is posted: ping reads the webhook back with a `GET` on its URL. See [the command line](../operations/cli.md#crier-ping).

## Animated GIFs

A GIF is attached exactly as it is; Discord animates it inline.

Set `render.video.format: gif` — see [video](../rendering/video.md).

Configuration keys: [`publish.discord.*`](../configuration/reference/publish-discord.md).
