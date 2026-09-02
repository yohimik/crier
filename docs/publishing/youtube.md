# YouTube

```yaml
render:
  video:
    enabled: true
    duration: 20s

publish:
  youtube:
    enabled: true
    privacy-status: private
```

```sh
export CRIER_PUBLISH_YOUTUBE_CLIENT_ID="…"
export CRIER_PUBLISH_YOUTUBE_CLIENT_SECRET="…"
export CRIER_PUBLISH_YOUTUBE_REFRESH_TOKEN="…"
crier ping     # are the credentials right, and is there a channel?
crier
```

## Videos only

This is the one platform in crier that takes no pictures.

The YouTube Data API v3 uploads videos. That is the whole of it. A community post is where a still image would go on a channel, and community posts have no public API at all. There is no endpoint to call.

So a run has to produce a video. Set `render.video.enabled`, or hand crier a clip with `publish.input`. A run of image posts with `youtube` enabled is refused before anything is staged, and the refusal names the platform:

```
crier: youtube: this platform does not take image
```

Animated GIFs are refused for the same reason. A GIF uploaded through `videos.insert` becomes a silent clip rather than an animation, which is worse than a clear no. See [video](../rendering/video.md).

## Uploads are forced private until Google audits your project

**Read this before you set `privacy-status: public`.**

A new Google Cloud project using the YouTube Data API is unverified. Every video uploaded from an unverified project is locked to **private**, whatever the request asks for, and it stays private. This is not a bug and there is no flag for it. It is how Google limits unaudited API clients.

The API accepts `privacyStatus: public`, answers 200, and files the video as private anyway. Nothing in the response says so.

To lift it, submit the project for the YouTube API compliance audit from the Google Cloud console. The review takes weeks. Until it passes, treat every upload as private, which is why `publish.youtube.privacy-status` defaults to `private`: the default should match what actually happens.

## Getting the credentials

There is no token to paste. YouTube uses OAuth 2.0, and crier trades a refresh token for a fresh access token at the start of every run.

1. Open the [Google Cloud console](https://console.cloud.google.com/) and create a project.
2. Under **APIs & Services → Library**, enable **YouTube Data API v3**.
3. Under **OAuth consent screen**, pick **External**, fill in the app name and the support email, and add yourself under **Test users**. A consent screen in testing mode issues refresh tokens that expire after seven days, so publish the app once it works.
4. Under **Credentials**, create an **OAuth client ID** of type **Web application**. Add `https://developers.google.com/oauthplayground` as an authorised redirect URI. Copy the client id and the client secret.
5. Open the [OAuth 2.0 Playground](https://developers.google.com/oauthplayground/). In the settings cog, tick **Use your own OAuth credentials** and paste the two values.
6. In the scope box on the left, enter the scope by hand:

   ```
   https://www.googleapis.com/auth/youtube.upload
   ```

   That scope uploads videos and sets thumbnails. It cannot read anything else, which is the point.
7. Authorise, then exchange the authorisation code for tokens. Copy the **refresh token**.

```sh
export CRIER_PUBLISH_YOUTUBE_CLIENT_ID="1234-abc.apps.googleusercontent.com"
export CRIER_PUBLISH_YOUTUBE_CLIENT_SECRET="GOCSPX-…"
export CRIER_PUBLISH_YOUTUBE_REFRESH_TOKEN="1//0g…"
```

A refresh token is revoked when the account's password changes, when the consent is withdrawn, and after seven days while the consent screen is still in testing. Those are the three reasons a setup that worked stops working.

If your channel is a Brand Account, authorise while that channel is the one selected in the account picker. A refresh token issued for the personal account uploads to the personal channel.

## How an upload is made

1. `POST {auth-base-url}/token` with the client id, the client secret, the refresh token and `grant_type=refresh_token`. This happens once per run, at first use, and the access token is held in memory for the rest of it.
2. `POST /upload/youtube/v3/videos?uploadType=resumable&part=snippet,status` with the title, the description, the category and the privacy status as JSON. YouTube answers with an upload session URL in the `Location` header.
3. `PUT` the video bytes to that URL, streamed from disk. YouTube answers with the video resource, and its `id` is the video.

Steps 2 and 3 are never retried on a 5xx. The session URL is single-use state, and a `PUT` that arrived and whose answer was lost would become a second video if it were sent again. The resumable protocol's own resume dance, a `Content-Range` probe followed by the remaining bytes, is a follow-up rather than something a twenty second clip needs: repeating a short upload is cheaper than resuming it.

A refused token is a credential failure, named as one. The message says which of the three values to look at, because Google's own answer is `invalid_grant` and nothing else.

## The title and the description

| | Where it comes from |
| --- | --- |
| Title | `publish.youtube.title`, then the caption's first line, then `crier` |
| Description | `publish.youtube.caption`, then the shared `publish.caption` |

Both are [templates](../templates/captions.md) like every other post text.

YouTube caps a title at **100 characters** and refuses anything longer. crier truncates instead and logs it at debug level. The title is usually a line lifted out of the caption, and losing its tail beats losing the upload. Set `publish.youtube.title` to choose one yourself.

A title cannot contain `<` or `>`. YouTube refuses the whole upload over either. crier removes them and logs at debug level, so a caption written with a `<v2>` in it still goes out.

## Shorts

A Short is not a separate endpoint, a separate upload or a separate setting. YouTube decides on its own, from the file and the text:

- The video is **vertical or square**.
- It is **three minutes or shorter**.
- The title or the description contains **`#Shorts`**.

So a Short is a matter of what you configure:

```yaml
render:
  video:
    enabled: true
    duration: 30s

publish:
  caption: "{{ .title }} {{ .version }} is out\n\n#Shorts"
  youtube:
    width: 1080
    height: 1920
    fit: cover
```

`fit: cover` crops the master render into the vertical frame rather than reflowing it. See [fitting the platform](../templates/overlays.md#fitting-the-platform).

## A custom thumbnail

```yaml
publish:
  youtube:
    thumbnail: cover.jpg
```

The file is uploaded after the video, through `POST /upload/youtube/v3/thumbnails/set`. JPEG and PNG are taken, chosen by the extension.

**A custom thumbnail needs a phone-verified channel.** YouTube answers 403 for a channel that is not verified, and verification is a one-off step at [youtube.com/verify](https://www.youtube.com/verify).

A refusal here is a **warning, not an error**. The video is already up by then, and reporting a failed run would say the upload did not happen. The reason is logged and the run reports success:

```
WRN the video was uploaded but the custom thumbnail was refused video=dQw4w9WgXcQ
```

## Quota

The Data API has a default quota of **10,000 units a day**, and `videos.insert` costs **1,600 units**. That is about **six uploads a day**, and it is a per-project budget rather than a per-channel one.

`thumbnails/set` costs 50 units. The `channels.list` call `crier ping` makes costs 1.

A quota raise is requested from the Google Cloud console, in the same place as the compliance audit. Running out of quota looks like this:

```
403: The request cannot be completed because you have exceeded your quota.
```

The quota resets at midnight Pacific time.

## Limits worth knowing

| | Limit |
| --- | ----- |
| Video | 256GB, or 12 hours |
| Uploads | about 6 a day on the default quota |
| Title | 100 characters |
| Description | 5000 characters |
| Thumbnail | 2MB, JPEG or PNG, and a phone-verified channel |
| Files per upload | 1 |

There is no carousel here, so `publish.youtube.max-attachments` has nothing to lower. A value above 1 is a configuration error rather than something silently clamped. Long content does not paginate into several videos either: a clip is one file however many pages the stills would have been. See [pagination](../rendering/pagination.md).

## Categories

`publish.youtube.category-id` is YouTube's own category number. The default is `22`, "People & Blogs", which is the safe answer for anything that is not obviously one of the others. `28` is "Science & Technology". The full list comes from `videoCategories.list` and varies by region.

## Check it

```sh
crier ping
```

Nothing is uploaded. Ping refreshes the token, which is the half of a YouTube setup that actually goes wrong, then reads `GET /youtube/v3/channels?part=snippet&mine=true` and reports the channel name.

A Google account is not a channel. If the account has never created one, the token works, the call succeeds, and it comes back with no items. The row says so rather than passing quietly:

```
the token works and the account has no youtube channel; create one at youtube.com and authorise again
```

That is the way a YouTube setup passes every credential check and then fails at the upload. See [the command line](../operations/cli.md#crier-ping).

## What you get back

The video id and its watch link, built as `https://www.youtube.com/watch?v=<id>`. The link works as soon as the upload finishes, though a private video only opens for the account that owns it.

## Music

A YouTube video carries its own audio track, so there is nothing to attach beside it. Use `render.video.audio` to mix a track into the file, which every platform then gets. `publish.youtube.music-file` and `publish.youtube.lead-video` are refused with a reason rather than ignored. See [music](./music.md).

Configuration keys: [`publish.youtube.*`](../configuration/publish/youtube.md).
