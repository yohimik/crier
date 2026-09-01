# Slack

```yaml
publish:
  slack:
    enabled: true
    channel: C0123ABCD
```

```sh
export CRIER_PUBLISH_SLACK_TOKEN=xoxb-…
crier ping     # is the token right?
crier
```

Slack takes the bytes, so it needs no [staging](../staging/README.md).

## Getting a token

1. Create an app at [api.slack.com/apps](https://api.slack.com/apps) — **From
   scratch**, and pick the workspace.
2. Under **OAuth & Permissions**, add two **bot token scopes**:

   | Scope | What it is for |
   | ----- | -------------- |
   | `files:write` | uploading the image or clip |
   | `chat:write` | posting the caption alongside it |

3. **Install to Workspace**, and copy the **Bot User OAuth Token**. It starts
   with `xoxb-`.
4. **Invite the bot to the channel.** This is the step people miss:

   ```
   /invite @your-app
   ```

   A bot that is not in the channel gets `not_in_channel`, which crier reports
   with that instruction attached.

## The channel is an ID

`publish.slack.channel` wants a channel ID like `C0123ABCD`, not `#general`.
Right-click the channel, **View channel details**, and the ID is at the bottom
of the panel. A name gets `channel_not_found`.

## How it goes out

Three calls, because the one-call method is gone: `files.upload` was retired,
and its replacement splits the work into handing out an upload slot, taking the
bytes, and being told what the file is for.

1. `POST /files.getUploadURLExternal` with `filename` and `length`, carrying
   the bot token. Slack answers with an `upload_url` and a `file_id`.
2. `POST` the raw bytes to that `upload_url`, as
   `application/octet-stream`. **No token**: the URL is the credential, and an
   `Authorization` header sent there is a request rejected by a host that never
   wanted one. The declared `length` has to match what is sent.
3. `POST /files.completeUploadExternal` with the `file_id`, the `channel_id`
   and the caption as `initial_comment`. This is the step that posts, so crier
   does not retry it: a 5xx from a gateway may still have shared the file.

Verified against Slack's method reference on 2026-09-01.

## `ok: false` inside a 200

Slack reports an application error with HTTP 200 and `"ok": false`, so the
status code alone says nothing. crier reads the envelope and translates the
ones worth explaining:

| Slack says | crier says |
| ---------- | ---------- |
| `not_in_channel` | the bot is not in that channel; invite it with `/invite @your-app` |
| `invalid_auth`, `not_authed`, `token_revoked` | slack refused the token; check `publish.slack.token` |
| `missing_scope` | the token is missing a scope; it needs `files:write` and `chat:write` |
| `channel_not_found` | no such channel; `publish.slack.channel` wants an ID like `C0123ABCD` |

## What you get back

The file id, and the channel in `extra`. Slack does not return a permalink from
`files.completeUploadExternal` and the file id alone is not a URL anyone can
open, so crier reports no link rather than a guessed one.

## Video and animated GIFs

Both work, as files like any other — Slack plays them inline with no
transcoding and no separate endpoint. See [video](../rendering/video.md).

## Check it

```sh
crier ping
```

Nothing is posted: ping calls `auth.test`, which needs no scope at all. That is
what makes it the right check — it separates a token Slack has never heard of
from one that merely cannot do what crier wants. The row names the user and the
workspace.

`auth.test` says nothing about channel membership, which is the other half of a
working setup, so the row's note says which channel was configured and admits
it could not be confirmed.

Configuration keys: [`publish.slack.*`](../configuration/reference/publish-slack.md).
