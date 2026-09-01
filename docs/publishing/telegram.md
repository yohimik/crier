# Telegram

The simplest of the nine: one multipart request, no container, no polling, and
it takes the bytes rather than a URL.

## Setting it up

1. Message [@BotFather](https://t.me/BotFather), `/newbot`, and keep the token.
2. Add the bot to the channel or group as an administrator.
3. The chat id is `@channelusername` for a public channel, or the numeric id
   for anything else — `@userinfobot` will tell you.

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

crier checks both before uploading.

## Video

`sendVideo` with `supports_streaming=true`. Nothing to configure.

## What you get back

The message id, and a `https://t.me/<channel>/<id>` link when the chat has a
username.

## Check it

```sh
crier ping
```

Nothing is posted: ping calls `getMe`. See [the command line](../operations/cli.md#crier-ping).

Configuration keys: [`publish.telegram.*`](../configuration/reference/publish-telegram.md).
