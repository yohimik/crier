# Custom platforms

Anything with a shell and an HTTP client can be a crier platform. A custom platform is a shell command. First, crier renders the image. Next, it tells the command where the image is and what to say. Finally, the command does the posting.

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

`my-webhook` is a name you choose. It is a peer of the ten built-in platforms, not a lesser thing. It takes part in the fan-out. It gets its own [overlay and size](../templates/overlays.md). It gets its [caption templated](../templates/captions.md). It appears in `crier ping` and in a `--dry-run` listing. Its failure is a partial failure like any other.

## The contract

crier runs `sh -c <command>` and reads the result out of a file.

### What the command is told

| Variable | What it holds |
| -------- | ------------- |
| `CRIER_PLATFORM` | the name you gave this platform |
| `CRIER_ARTIFACT` | the file to publish; the first one when a post carries several |
| `CRIER_ARTIFACTS` | every file this post carries, one per line, in page order |
| `CRIER_ARTIFACT_COUNT` | how many files that is |
| `CRIER_ARTIFACT_KIND` | `image` or `video` |
| `CRIER_ARTIFACT_FORMAT` | `png` or `jpeg`; empty for a video |
| `CRIER_ARTIFACT_TYPE` | the MIME type |
| `CRIER_CAPTION` | the rendered post text |
| `CRIER_URL` | where the file was staged, **only** when `needs-url` is on |
| `CRIER_URLS` | where each file was staged, one per line, same order, **only** when `needs-url` is on |
| `CRIER_POSTER` | a still image for a video, when there is one |
| `CRIER_OUTPUT` | a file to append the result to |
| `CRIER_POST` · `CRIER_POSTS` | which post of how many this is |
| `CRIER_PAGE` · `CRIER_PAGES` | the first page this post carries, and how many the run produced |

When `needs-url` is off, `CRIER_URL` is absent rather than empty. This lets a script tell "no URL was needed" from "staging produced nothing".

## Several files at once

A template whose content overflows its page [paginates](../rendering/pagination.md). By default your command runs once per page. Set `max-attachments` to take more than one at a time:

```yaml
publish:
  custom:
    mine:
      command: sh ./publish.sh
      max-attachments: 10
```

`CRIER_ARTIFACTS` then holds up to ten paths, one per line, and `CRIER_ARTIFACT_COUNT` says how many. One per line rather than space separated, because a path may contain a space:

```sh
#!/bin/sh
set -eu
args=""
while IFS= read -r file; do
	args="$args -F file[]=@$file"
done <<EOF
$CRIER_ARTIFACTS
EOF
# shellcheck disable=SC2086
curl -fsS $args -F "text=$CRIER_CAPTION" "$WEBHOOK"
```

`CRIER_ARTIFACTS` always holds at least the file `CRIER_ARTIFACT` names, so a script can read it alone and never look at the singular form.

A page list longer than `max-attachments` runs the command more than once, in page order, and stops at the first failure. `CRIER_POST` and `CRIER_POSTS` say where in that sequence a run sits. They read `1` and `1` when nothing paginated.

Everything in `env:` is set too, right after crier's own variables. This means a token lives in the configuration or the environment rather than inside the command string. The whole process environment is inherited. Variables like `$HOME`, `$PATH`, and anything your CI exports are all there.

### What the command answers with

**Exit 0 means published.** That is the whole requirement. A script that only exits 0 is a valid publisher.

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

Append rather than overwrite. The file exists before the command starts. A script can add to it as it goes.

**A non-zero exit is a failure.** The last of the command's output goes into the error. A script that prints why it failed gets that reported. Both streams are logged live at debug level.

## Keys

| Key | Default | What it does |
| --- | ------- | ------------ |
| `enabled` | `false` | publish through this platform |
| `command` | none | the shell command; required when enabled |
| `ping-command` | none | what `crier ping` runs instead |
| `caption` | none | this platform's caption, overriding `publish.caption` |
| `kinds` | `image` | what the command accepts: `image`, `video`, `gif`, or any combination |
| `format` | `png` | preferred image format |
| `needs-url` | `false` | stage the file and pass `CRIER_URL` |
| `timeout` | `2m` | how long the command may run before it is killed |
| `overlay` | none | extra template overlays, as for a built-in |
| `width`, `height` | none | render size for this platform |
| `env` | none | extra environment variables, keys kept as written |


## Names

A name consists of lower-case letters, digits and dashes. It cannot be one of the nine built-ins.

This rule exists because a name must survive a round trip through an environment variable. For example, `publish.custom.my-hook.command` becomes `CRIER_PUBLISH_CUSTOM_MY_HOOK_COMMAND`. Using `my_hook` would result in the same variable and a different platform.

## Setting it three ways

You can set this like every other key: in a file, in the environment, or as a flag:

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

`--set` exists because a flag cannot be registered for a name crier has never heard of. It works for every other key too. See [configuration](../configuration/README.md).

Any of the three can introduce a platform. A name found in the environment or in a `--set` is as real as one written in the file. This makes a CI-only platform possible.

The keys **under** a name are still closed. A typo in `commnad` is refused. The error names its full path, exactly as it would anywhere else in the configuration.

## Checking it without posting

```sh
crier ping
```

A custom platform without a `ping-command` reports that there is nothing to check. It does not run `command` because that would publish the post. Give it a read-only call, and it reports just like the built-ins do:

```yaml
      ping-command: |
        curl -sf "$WEBHOOK_URL/me" -o /tmp/me.json
        echo "id=$(jq -r .id /tmp/me.json)" >> "$CRIER_OUTPUT"
        echo "name=$(jq -r .name /tmp/me.json)" >> "$CRIER_OUTPUT"
```

The `id` and `name` become the account column of the ping report.

## Windows

The command runs under `sh` on every platform. This means one configuration works everywhere. On Windows, `sh.exe` has to be on `PATH`. Git for Windows or WSL provides one. Without it, an enabled custom platform is a configuration error that says so. This avoids a "file not found" halfway through a publish.

## A complete example

[`examples/custom-platform/`](../../examples/custom-platform/) is a working project. It includes a template, a config, and a script. The script posts to a webhook with `curl`.

You can find the configuration keys at [`publish.custom.*`](../configuration/publish/custom.md).
