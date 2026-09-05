# Discord

This is an incoming webhook. The URL **is** the credential. Anyone holding it can post to that channel. This is why the URL is the secret, rather than a token beside it.

## Setting it up

1. Go to Channel, click Edit Channel, select Integrations, choose Webhooks, and click New Webhook.
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

A free server allows 10MB per attachment. A boosted server allows more. crier warns you at the free-tier limit instead of treating it as absolute.

## Mentions

The caption is posted as the message content. Crier leaves Discord's ordinary mention behavior unchanged by default. An `@everyone` or `@here` notification must be enabled explicitly:

```yaml
publish:
  discord:
    caption: "{{ .product }} {{ .version }} is out @everyone"
    mention-everyone: true
```

The same setting is available as `--publish-discord-mention-everyone` and `CRIER_PUBLISH_DISCORD_MENTION_EVERYONE`. Keep it false for captions that should not notify the whole channel.

## What you get back

You get the message id and its channel. This is because crier appends `?wait=true`. Without it, Discord answers 204 and says nothing.

## Check it

```sh
crier ping
```

This does not post anything. Instead, ping reads the webhook back with a `GET` request on its URL. See [the command line](../operations/cli.md#crier-ping).

## Animated GIFs

The GIF is attached exactly as it is. Discord animates it inline.

Set `render.video.format: gif`. See [video](../rendering/video.md).

A message carries up to ten files. See [pagination and carousels](../rendering/pagination.md).

## Music

Set `publish.music-file` and the track becomes another attachment in the same message, which Discord plays inline. It takes one of the ten slots, so a run with music posts at most nine pages per message. See [music](./music.md).

Configuration keys: [`publish.discord.*`](../configuration/publish/discord.md).
