# Telegram

This is the simplest of the twelve. It requires one multipart request. There is no container and no polling. It takes the bytes rather than a URL.

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

## Music

Set `publish.music-file` and the track goes out as a `sendAudio` message immediately after the post, in the same chat, which clients render as a player under the album. It has to be its own message: the Bot API groups audio only with other audio, so a track cannot join an album of pictures. That message failing is a warning, not a failed post. See [music](./music.md).

## Opening with a video

Set `publish.telegram.lead-video` and the album opens with the clip: an `InputMediaVideo` at the head of the media group, which is the one Telegram shape that mixes a video with photos. It takes one of the album's ten slots, and the caption travels with it because an album's caption belongs to its first item. A post can open with a clip and still carry a track after it. See [music](./music.md#a-video-that-opens-the-post).

Configuration keys: [`publish.telegram.*`](../configuration/publish/telegram.md).
