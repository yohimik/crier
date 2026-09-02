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

Slack takes the bytes. It does not need [staging](../staging/README.md).

## Getting a token

1. Create an app at [api.slack.com/apps](https://api.slack.com/apps). Choose **From scratch**, and pick the workspace.
2. Under **OAuth & Permissions**, add two **bot token scopes**:

   | Scope | What it is for |
   | ----- | -------------- |
   | `files:write` | uploading the image or clip |
   | `chat:write` | posting the caption alongside it |

3. Click **Install to Workspace**. Copy the **Bot User OAuth Token**. It starts with `xoxb-`.
4. **Invite the bot to the channel.** People often miss this step:

   ```
   /invite @your-app
   ```

   A bot that is not in the channel gets `not_in_channel`. crier reports this and attaches the instruction.

## The channel is an ID

You need to provide a channel ID like `C0123ABCD` for `publish.slack.channel`. Do not use a name like `#general`. Right-click the channel and select **View channel details**. You will find the ID at the bottom of the panel. If you pass a name instead, you will get a `channel_not_found` error.

## How it goes out

You must make three calls. The one-call method is gone. Slack retired `files.upload`. The replacement splits the work into three steps. It hands out an upload slot, takes the bytes, and learns what the file is for.

1. Send `POST /files.getUploadURLExternal` with the `filename` and `length`. You must include the bot token. Slack answers with an `upload_url` and a `file_id`.
2. `POST` the raw bytes to that `upload_url` as `application/octet-stream`. Send **No token**. The URL is the credential. If you send an `Authorization` header, the host will reject the request. The declared `length` must match the bytes you send.
3. Send `POST /files.completeUploadExternal` with the `file_id`, the `channel_id`, and the caption as `initial_comment`. This step posts the file. For this reason, crier does not retry it. A 5xx error from a gateway might mean the file was still shared.

This was verified against Slack's method reference on 2026-09-01.

## `ok: false` inside a 200

Slack reports an application error with HTTP 200 and `"ok": false`. The status code alone says nothing. crier reads the envelope and translates the ones worth explaining:

| Slack says | crier says |
| ---------- | ---------- |
| `not_in_channel` | the bot is not in that channel; invite it with `/invite @your-app` |
| `invalid_auth`, `not_authed`, `token_revoked` | slack refused the token; check `publish.slack.token` |
| `missing_scope` | the token is missing a scope; it needs `files:write` and `chat:write` |
| `channel_not_found` | no such channel; `publish.slack.channel` wants an ID like `C0123ABCD` |


## What you get back

You get the file id, and the channel in `extra`. Slack does not return a permalink from `files.completeUploadExternal`. The file id alone is not a URL anyone can open. Because of this, crier reports no link rather than a guessed one.

## Video and animated GIFs

Both work like any other file. Slack plays them inline with no transcoding and no separate endpoint. See [video](../rendering/video.md).

## Check it

```sh
crier ping
```

Nothing is posted. Ping calls `auth.test`, which needs no scope at all. That is what makes it the right check: it separates a token Slack has never heard of from one that merely cannot do what crier wants. The row names the user and the workspace.

`auth.test` says nothing about channel membership. This is the other half of a working setup. As a result, the row's note says which channel was configured. It also admits this could not be confirmed.

Configuration keys: [`publish.slack.*`](../configuration/publish/slack.md).
