# Music

A post can carry an audio file. Three platforms accept one, and one platform can pick a track of its own.

```yaml
publish:
  music-file: jingle.mp3
```

The path is resolved against the configuration file, like every other path key. The file is checked before anything is rendered.

## There is no track id, anywhere

This is the first thing to know, because looking for a way to name a licensed song is a long search that ends in nothing.

**No public API takes the id of a licensed track.** Not Instagram, not Facebook, not TikTok. The music pickers those apps show live inside the apps. There is no endpoint behind them. The Meta Sound Collection cannot be reached from a program. Neither can TikTok's Commercial Music Library. Instagram's `audio_name` field only renames audio that was already inside the video you uploaded.

So crier has no `music-track-id` key. There is nothing for one to hold.

What is left is an audio file you send yourself, and one TikTok flag that asks TikTok to choose.

## What each platform does

| Platform | What happens | How |
| --- | --- | --- |
| [Discord](./discord.md) | the track is another attachment in the same message | one more `files[n]` part |
| [Slack](./slack.md) | the track is another file in the same message | one more upload, shared in the same call |
| [Telegram](./telegram.md) | the track is a second message, right after the post | `sendAudio` in the same chat |
| [TikTok](./tiktok.md) | TikTok adds a track it recommends | `auto_add_music` on a photo post |
| Instagram, Facebook, X, Mastodon, LinkedIn, Reddit | nothing | there is no API for it |

Discord and Slack show a player inline. Telegram renders the audio message as a player under the album. Instagram and Facebook get nothing this way: a soundtrack reaches Instagram only inside a video, either as a [lead video](#a-video-that-opens-the-post) or a [generated cover Story](#a-cover-story-beside-the-feed-post).

## A cover Story beside the feed post

Set `publish.instagram.cover-story: true` with `render.video.audio` or `render.video.audio-pool`. Crier keeps the normal Instagram feed post or carousel and turns its first page into a separate 16-second MP4 Story with the selected track. It loops shorter audio to fill the clip. The photo document is rendered once.

This mode needs the same public staging as every Instagram publication. It cannot be combined with `publish.instagram.story: true`, which makes the primary output story-only. See [Instagram](./instagram.md#add-a-music-backed-cover-story-to-a-feed-post).

Telegram is a second message because it has to be. The Bot API groups audio only with other audio, so a track cannot join the album of pictures it belongs to. crier sends the pictures, waits for them to land, and sends the audio next. That message failing is a warning rather than a failure of the post. The pictures are already out, and there is no taking them back.

## Rights

Attach only audio you may distribute.

A file crier sends is a file you are publishing. The three platforms treat it as your upload, not as a licensed track drawn from their catalogue, so their music licences do not cover it. Use something you wrote, something you bought a distribution licence for, or something released under a licence that permits it. A track ripped from a streaming service is none of those.

This is also why the licensed pickers are in-app only. The platforms hold those licences and will not extend them to an API.

## Per platform

A platform can name a file of its own:

```yaml
publish:
  music-file: jingle.mp3          # discord and slack get this
  telegram:
    music-file: long-version.mp3  # telegram gets this instead
```

Setting `music-file` on any of the other eleven platforms is a configuration error. The message names the platform and says why. Nothing is refused for the shared `publish.music-file` key: it means the file for the platforms that can take it, and stays quiet about the rest.

If no enabled platform can carry the file, crier warns. The run still goes ahead.

## Formats

crier reads the first bytes of the file and refuses anything it does not recognise. The extension is not consulted. An image renamed to `.mp3` is accepted by the file system and refused by the platform, after the post.

| Container | Sent as |
| --- | --- |
| mp3 | `audio/mpeg` |
| m4a | `audio/mp4` |
| ogg | `audio/ogg` |
| wav | `audio/wav` |

Telegram documents mp3 and m4a for `sendAudio`. An ogg or a wav is usually accepted and then shown as a plain file rather than as a player. crier warns when the file is one of those and sends it anyway.

## Size, and the file count

The audio takes one of the message's file slots at Discord and at Slack. Both take ten files, so a run with music posts at most nine pages per message. crier reserves the slot rather than refusing the post later, which is what lets a long document paginate into messages that fit. See [pagination and carousels](../rendering/pagination.md).

Sizes are the platform's own. Discord takes 10MB per attachment on a free server. Telegram takes 50MB. An audio file over the limit is refused before it is sent, and at Telegram the post still goes out without it.

## TikTok picks its own

```yaml
publish:
  tiktok:
    auto-add-music: true
```

This asks TikTok to put a recommended track under a photo post. It is the only music setting any of these APIs offers, and it names no track. TikTok chooses one, and the account can change it afterwards inside the app.

It works on a photo post made with `DIRECT_POST`, which is the only kind crier makes. TikTok documents it nowhere else, so it is not sent on a video post.

## Music in a video

A clip is a different thing. Its audio is mixed in by ffmpeg while the video is encoded, so the track is inside the file every platform receives:

```yaml
render:
  video:
    enabled: true
    audio: theme.mp3
```

That is `render.video.audio`, and it is unrelated to `publish.music-file`. One is part of the render. The other is an attachment. See [video](../rendering/video.md#output).

A GIF has no audio track, so `render.video.audio` is ignored for one.

## A video that opens the post

This is one way a soundtrack reaches Instagram. A generated [cover Story](#a-cover-story-beside-the-feed-post) can carry the selected track beside an ordinary photo feed post instead.

```yaml
publish:
  instagram:
    lead-video: anthem.mp4
```

The clip becomes item one of the post. The pages follow it. A reader meets the video first and hears whatever is inside it, which is the only music Instagram will play from an API.

Two platforms take a post of mixed media:

| Platform | What happens | How |
| --- | --- | --- |
| [Instagram](./instagram.md) | the carousel's first child is the clip | a `video_url` child container, then the images |
| [Telegram](./telegram.md) | the album's first item is the clip | an `InputMediaVideo` at the head of the media group |
| the other ten | nothing | a post there is pictures or a video, never both |

Setting `lead-video` on any of the other ten is a configuration error. The message names the platform and says why.

The clip has to be an MP4. crier reads the first bytes and refuses anything else, for the same reason it does with audio.

### It costs an attachment

Both platforms take ten items, and the clip is one of them. A run with a lead video therefore posts at most nine pages per post. crier reserves the slot rather than refusing the post later, so a long document paginates into posts that fit. See [pagination and carousels](../rendering/pagination.md).

Every post of a sequence opens with the clip, not only the first. A reader meets each post on its own, and one that began with a page out of the middle would be the only one without an opening.

A single page plus a lead video is still a carousel of two. That is the point: one card and one clip are two items.

### Instagram

The child container is `video_url`, `is_carousel_item=true` and `media_type=VIDEO`. A child carries no caption, which Meta does not accept.

The `media_type` is worth a note, because the reference and the endpoint disagree. The reference lists `CAROUSEL`, `REELS` and `STORIES` as that parameter's values and does not mention `VIDEO` at all, which reads as an instruction to leave it out. Leave it out and the call fails: `400 IGApiException` code 100, "The parameter image_url is required". Without a `media_type` the API presumes an image child and asks for the parameter an image child would have. Send `VIDEO` and it works. Behaviour wins.

Instagram fetches the clip from a public URL like everything else it posts, so the lead video is staged the way the pages are. See [staging](../staging/README.md).

Video children are processed asynchronously. crier waits for the clip's container to report `FINISHED` before it creates the parent, which is the same wait every container gets.

One thing worth knowing about the shape: Instagram crops a carousel to the aspect ratio of its **first** item. With a lead video, that first item is the clip, so the clip's shape decides how the cards beside it are cropped. Render them to match.

Stories have no carousel, so a story pass ignores `lead-video` and says so in the log. Post the clip as its own story instead, with `publish.input`.

### Telegram

The clip is an `InputMediaVideo` at the head of the media group, which is the one Telegram shape that mixes a video with photos. Telegram takes the bytes, so nothing is staged.

The caption belongs to an album's first item, so with a lead video the caption travels with the clip. It still appears under the album exactly as before.

The audio file takes nothing away from this. `music-file` is a message of its own after the album, so a Telegram post can open with a clip and still have a track playing under it.

## Check it

```sh
crier ping
```

A configured music file gets a row of its own, and so does a lead video. The row says the file was found, what it is, how large it is, and which enabled platforms will carry it:

```
TARGET                STATUS  ACCOUNT      MS  DETAIL
music                 ok      jingle.mp3   0   mp3, 412.0kB; discord, slack
music:telegram        ok      long.mp3     0   m4a, 2.1MB; telegram
lead-video:instagram  ok      anthem.mp4   0   mp4, 4.2MB; opens the instagram post
```

One row per key, so a broken override says which line to change. Nothing is uploaded and nothing is posted.

Configuration keys: [`publish.music-file`](../configuration/publish/README.md), and `publish.<platform>.music-file` and `publish.<platform>.lead-video` on each platform's page.
