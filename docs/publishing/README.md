# Publishing

```sh
crier
```

renders, encodes, stages if it has to, and posts to every enabled platform.

Publishing is what crier does with no command, so `crier` and `crier publish`
are the same thing. Flags work either way: `crier --dry-run` is
`crier publish --dry-run`.

## What happens, in order

1. **The data document is loaded**, once.
2. **Publishers are built** from the configuration. A missing token is an error
   here — before a single request, and before the platforms that *are*
   configured have posted anything.
3. **Captions are resolved**, once per platform. See [captions](../templates/captions.md).
4. **Variants are rendered.** Platforms that agree on their overlays and size
   share one render. See [overlays](../templates/overlays.md).
5. **Formats are encoded**: the configured one, plus whatever a platform
   insists on — Instagram takes JPEG and nothing else.
6. **Staging**, if any enabled platform can only be given a URL. See
   [staging](../staging/README.md).
7. **The fan-out**, `publish.concurrency` at a time. One platform's failure
   never cancels another's: the whole point of posting to nine places is that
   eight of them still get the post when the ninth is down.
8. **The report**, on standard output.
9. **Cleanup** — staged objects deleted, the local server stopped, the tunnel
   killed, temporary files removed — on a context of its own, so it happens
   even after Ctrl-C.

## The report

```
PLATFORM   STATUS  ID          URL
discord    ok      1399…       https://discord.com/channels/…
instagram  ok      1798…       https://www.instagram.com/p/1798…
telegram   failed              Post "https://api.telegram.org/…": 400: chat not found
```

`--json` prints the same as an object, including the variants and the files.

## Exit codes

| Code | Meaning |
| ---- | ------- |
| 0 | every platform took the post |
| 4 | some did, some did not |
| 5 | none did |

See [exit codes](../operations/exit-codes.md) for the rest.

## Dry runs

```sh
crier --dry-run
```

renders everything, resolves every caption, and makes **no network calls**. It
prints what would be sent, per platform:

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

The shared HTTP client retries a 429 (honouring `Retry-After`), a 5xx and a
network error, with exponential backoff and jitter.

The call that actually creates the post does not get that treatment. A 5xx from
`media_publish`, `/2/tweets`, `/statuses` or `/api/submit` may mean the post was
created and the answer was lost; retrying it would publish twice. Those calls
retry a 429 — which means the request was refused and never ran — and nothing
else.

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

## Check a setup before you post

```sh
crier ping
```

Every enabled platform is asked who its credentials belong to, over a read-only
endpoint, and nothing is posted. It is the way to find out a token is wrong
that does not involve a real post on a real feed. See
[the command line](../operations/cli.md#crier-ping).

Configuration keys: [`publish.*`](../configuration/reference/publish.md).
