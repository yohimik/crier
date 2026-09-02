# Telegram

This is the simplest of the nine. It requires one multipart request. There is no container and no polling. It takes the bytes rather than a URL.

## Setting it up

1. Message [@BotFather](https://t.me/BotFather). Send `/newbot` and keep the token.
2. Add the bot to the channel or group as an administrator.
3. The chat id is `@channelusername` for a public channel. It is the numeric id for anything else. The `@userinfobot` will tell you this id.

```yaml
publish:
  telegram:
    enabled: true
    chat-id: "@my_channel"
```

```sh
export CRIER_PUBLISH_TELEGRAM_TOKEN="123456:ABC-DEF…"
```

## Limits

| | |
| --- | --- |
| Photo | 10MB |
| Any file | 50MB |

crier checks both limits before uploading.

## Video

Use `sendVideo` with `supports_streaming=true`. There is nothing to configure.

## What you get back

You get the message id. If the chat has a username, you also get a `https://t.me/<channel>/<id>` link.

## Check it

```sh
crier ping
```

Nothing is posted. The ping command calls `getMe`. See [the command line](../operations/cli.md#crier-ping).

## Animated GIFs

Send a GIF using `sendAnimation` instead of `sendVideo`. The `sendVideo` method accepts a GIF but shows it as a still image. This bug only appears on the feed.

Set `render.video.format: gif`. See [video](../rendering/video.md).

Several images go as one album through `sendMediaGroup`, up to ten, with the caption on the first. A single image still goes through `sendPhoto`, because a media group has a minimum of two. See [pagination and carousels](../rendering/pagination.md).

Configuration keys: [`publish.telegram.*`](../configuration/publish/telegram.md).
