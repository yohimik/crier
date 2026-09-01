# Custom platforms

Anything with a shell and an HTTP client can be a crier platform. A custom
platform is a shell command: crier renders the image, tells the command where
it is and what to say, and the command does the posting.

```yaml
publish:
  caption: "{{ .title }} — {{ .subtitle }}"
  custom:
    my-webhook:
      enabled: true
      command: |
        curl -sf -X POST "$WEBHOOK_URL" \
          -F "file=@$CRIER_ARTIFACT" \
          -F "text=$CRIER_CAPTION" \
          -o /tmp/reply.json
        echo "id=$(jq -r .id /tmp/reply.json)" >> "$CRIER_OUTPUT"
```

`my-webhook` is a name you choose. It is a peer of the nine built-in
platforms, not a lesser thing: it takes part in the fan-out, gets its own
[overlay and size](../templates/overlays.md), gets its
[caption templated](../templates/captions.md), appears in `crier ping` and in a
`--dry-run` listing, and its failure is a partial failure like any other.

## The contract

crier runs `sh -c <command>` and reads the result out of a file.

### What the command is told

| Variable | What it holds |
| -------- | ------------- |
| `CRIER_PLATFORM` | the name you gave this platform |
| `CRIER_ARTIFACT` | the file to publish |
| `CRIER_ARTIFACT_KIND` | `image` or `video` |
| `CRIER_ARTIFACT_FORMAT` | `png` or `jpeg`; empty for a video |
| `CRIER_ARTIFACT_TYPE` | the MIME type |
| `CRIER_CAPTION` | the rendered post text |
| `CRIER_URL` | where the file was staged — **only** when `needs-url` is on |
| `CRIER_POSTER` | a still image for a video, when there is one |
| `CRIER_OUTPUT` | a file to append the result to |

`CRIER_URL` is absent rather than empty when `needs-url` is off, so a script
can tell "no URL was needed" from "staging produced nothing".

Everything in `env:` is set too, after crier's own variables — so a token lives
in the configuration or the environment rather than inside the command string.
The whole process environment is inherited, so `$HOME`, `$PATH` and anything
your CI exports are all there.

### What the command answers with

**Exit 0 means published.** That is the whole requirement. A script that only
exits 0 is a valid publisher.

To report more, append `key=value` lines to `$CRIER_OUTPUT`:

```sh
echo "id=1234567" >> "$CRIER_OUTPUT"
echo "link=https://example.com/p/1234567" >> "$CRIER_OUTPUT"
```

| Key | Where it goes |
| --- | ------------- |
| `id` | the post id in the report |
| `link` (or `url`) | the post URL in the report |
| anything else | the report's `extra` object |

Append rather than overwrite: the file exists before the command starts, and a
script can add to it as it goes.

**A non-zero exit is a failure.** The last of the command's output goes into
the error, so a script that prints why it failed gets that reported. Both
streams are logged live at debug level.

## Keys

| Key | Default | What it does |
| --- | ------- | ------------ |
| `enabled` | `false` | publish through this platform |
| `command` | — | the shell command; required when enabled |
| `ping-command` | — | what `crier ping` runs instead |
| `caption` | — | this platform's caption, overriding `publish.caption` |
| `kinds` | `image` | what the command accepts: `image`, `video`, `gif`, or any combination |
| `format` | `png` | preferred image format |
| `needs-url` | `false` | stage the file and pass `CRIER_URL` |
| `timeout` | `2m` | how long the command may run before it is killed |
| `overlay` | — | extra template overlays, as for a built-in |
| `width`, `height` | — | render size for this platform |
| `env` | — | extra environment variables, keys kept as written |

## Names

A name is lower-case letters, digits and dashes, and may not be one of the nine
built-ins.

The rule exists because a name has to survive the round trip through an
environment variable: `publish.custom.my-hook.command` is
`CRIER_PUBLISH_CUSTOM_MY_HOOK_COMMAND`, and `my_hook` would be the same
variable and a different platform.

## Setting it three ways

Like every other key, in a file, in the environment, or as a flag:

```yaml
publish:
  custom:
    my-hook:
      command: "..."
```

```sh
export CRIER_PUBLISH_CUSTOM_MY_HOOK_ENABLED=true
export CRIER_PUBLISH_CUSTOM_MY_HOOK_COMMAND='curl ...'
export CRIER_PUBLISH_CUSTOM_MY_HOOK_ENV_TOKEN=s3cret
```

```sh
crier --set publish.custom.my-hook.enabled=true \
      --set publish.custom.my-hook.command='curl ...'
```

`--set` exists because a flag cannot be registered for a name crier has never
heard of. It works for every other key too — see
[configuration](../configuration/README.md).

Any of the three can introduce a platform: a name found in the environment or
in a `--set` is as real as one written in the file, which is what makes a
CI-only platform possible.

The keys **under** a name are still closed. A typo in `commnad` is refused
naming its full path, exactly as it would be anywhere else in the
configuration.

## Checking it without posting

```sh
crier ping
```

A custom platform with no `ping-command` reports that there is nothing to check
rather than running `command`, which would publish. Give it a read-only call
and it reports like the built-ins do:

```yaml
      ping-command: |
        curl -sf "$WEBHOOK_URL/me" -o /tmp/me.json
        echo "id=$(jq -r .id /tmp/me.json)" >> "$CRIER_OUTPUT"
        echo "name=$(jq -r .name /tmp/me.json)" >> "$CRIER_OUTPUT"
```

`id` and `name` become the account column of the ping report.

## Windows

The command runs under `sh`, on every platform, so one configuration works
everywhere. On Windows that means `sh.exe` has to be on `PATH` — Git for
Windows or WSL provides one. Without it, an enabled custom platform is a
configuration error that says so, rather than a "file not found" halfway
through a publish.

## A complete example

[`examples/custom-platform/`](../../examples/custom-platform/) is a working
project: a template, a config, and a script that posts to a webhook with
`curl`.

Configuration keys: [`publish.custom.*`](../configuration/reference/publish-custom.md).
