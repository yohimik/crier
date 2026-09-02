# Publishing

```sh
crier
```

It renders, encodes, and stages if it has to. Then it posts to every enabled platform.

Publishing is the default action when you provide no command. Running `crier` is the same as running `crier publish`. Flags work either way. For example, `crier --dry-run` is exactly the same as `crier publish --dry-run`.

## What happens, in order

1. **The data document is loaded**, once.
2. **Publishers are built** from the configuration. A missing token is an error here. This happens before a single request, and before the platforms that *are* configured have posted anything.
3. **Captions are resolved**, once per platform. See [captions](../templates/captions.md).
4. **Variants are rendered.** Platforms that agree on their overlays and size share one render. See [overlays](../templates/overlays.md).
5. **Formats are encoded**: the configured one, plus whatever a platform insists on. Instagram takes JPEG and nothing else.
6. **Staging**, if any enabled platform can only be given a URL. See [staging](../staging/README.md).
7. **The fan-out**, `publish.concurrency` at a time. One platform's failure never cancels another's. The whole point of posting to ten places is that eight of them still get the post when the ninth is down.
8. **The report**, on standard output.
9. **Cleanup**: staged objects deleted, the local server stopped, the tunnel killed, temporary files removed. This runs on a context of its own, so it happens even after Ctrl-C.

## The report

```
PLATFORM   STATUS  ID          URL
discord    ok      1399…       https://discord.com/channels/…
instagram  ok      1798…       https://www.instagram.com/p/1798…
telegram   failed              Post "https://api.telegram.org/…": 400: chat not found
```

Use `--json` to print the same output as an object. It includes the variants and the files.

## Exit codes

| Code | Meaning |
| ---- | ------- |
| 0 | every platform took the post |
| 4 | some did, some did not |
| 5 | none did |

Check [exit codes](../operations/exit-codes.md) for the rest.

## Dry runs

```sh
crier --dry-run
```

This renders everything and resolves every caption. It makes **no network calls**. It prints what would be sent per platform:

```
PLATFORM  VARIANT    FILE            URL NEEDED  CAPTION
discord   telegram-… /tmp/…/a.png    false       crier v1.2.3 is out on discord
```
## Which platforms are ready

```sh
crier platforms
```

```
PLATFORM   ENABLED  CONFIGURED  NEEDS URL  KINDS        PROBLEM
discord    true     true        false      image,video
instagram  false    false       true       image,video  publish.instagram.token is required…
```
## Retries, and the ones crier will not retry

The shared HTTP client retries a 429, a 5xx, and a network error. It honours `Retry-After`. It uses exponential backoff and jitter.

The call that actually creates the post does not get that treatment. A 5xx from `media_publish`, `/2/tweets`, `/statuses`, or `/api/submit` may mean the post was created and the answer was lost. Retrying it would publish twice. Those calls retry a 429, which means the request was refused and never ran. They retry nothing else.

## The platforms

| | Takes bytes | Needs a URL | Video | Notes |
| --- | --- | --- | --- | --- |
| [Instagram](./instagram.md) | no | **yes** | yes | JPEG only, and Meta fetches the URL itself |
| [Facebook](./facebook.md) | yes | optional | yes | Page posts and stories |
| [TikTok](./tiktok.md) | video only | photos | yes | app audit required |
| [Telegram](./telegram.md) | yes | no | yes | the simplest of the nine |
| [X](./x.md) | yes | no | yes | paid API tier |
| [Mastodon](./mastodon.md) | yes | no | yes | per-instance token |
| [Discord](./discord.md) | yes | no | yes | the webhook URL is the credential |
| [LinkedIn](./linkedin.md) | yes | no | yes | two mandatory headers |
| [Reddit](./reddit.md) | yes | link mode | yes | descriptive User-Agent required |
| [Slack](./slack.md) | yes | no | yes | bot token, and the bot must be in the channel |

## Four ways in

The pipeline has four entrances, not one. Crier can publish a file you already have (`publish.input`). It can also encode frames you already made (`render.video.frames-input`). This works without rendering anything. See [flows](./flows.md).

## One card, many shapes

There are two ways to give a platform a different shape. An [overlay](../templates/overlays.md) redraws the layout for it. A [fit](../templates/overlays.md#fitting-the-platform) resamples what was drawn. 

You can set `publish.<platform>.fit` to `cover`, `contain` or `stretch`. This is used for Instagram stories.

## Animated GIFs

Six of the ten platforms accept them: Telegram, Discord, Mastodon, X, Reddit and Slack. Instagram, Facebook, TikTok and LinkedIn do not. A `gif` aimed at one of them is refused before anything is rendered. You can find the table and how each of the five wants it in [video](../rendering/video.md#which-platforms-take-which).

## A platform crier does not have

Any shell command can be one. `publish.custom.<name>` defines a script-backed platform. It is a peer of the nine above. It has the same fan-out, same overlays, and same caption templating. It also has the same `crier ping`. See [custom platforms](./custom.md).

## Check a setup before you post

```sh
crier ping
```

It asks every enabled platform who its credentials belong to. It uses a read-only endpoint, so nothing is posted. This is how you find out a token is wrong without making a real post on a real feed. See [the command line](../operations/cli.md#crier-ping).

Configuration keys: [`publish.*`](../configuration/publish/README.md).
