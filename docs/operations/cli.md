# The command line

crier is one binary with a handful of commands. It publishes when you run it with no command at all. This means your everyday invocation has no words in it:

```sh
cd my-project && crier
```

## Dispatch

Here is the rule crier applies to its first argument:

| First argument | What runs |
| -------------- | --------- |
| nothing | `publish` |
| something starting with `-` | `publish`, with that flag |
| `--version` or `-version` | the version, then exit |
| `-h`, `--help` | the top-level usage |
| a known command word | that command |
| an unknown bare word | a usage error, exit 2 |

`--version` is a special case. It is handled ahead of the leading-dash rule. Without this, it would be handed to `publish`. The `publish` command has never heard of this flag.

## Nothing is guessed

crier fails closed. Its configuration decoder works the same way. There is no shorthand. There is no prefix matching. It does not pass through arguments it does not expect:

| What you typed | What happens |
| -------------- | ------------ |
| an unknown command word | exit 2, and the valid ones are listed |
| an unknown flag, anywhere | exit 2, naming the flag |
| a positional argument | exit 2; no command takes one |
| `--set` with a key that does not exist | exit 1, suggesting the nearest key |
| an unknown key in a config file | exit 1, naming its full path |

The reason is `--dry-runn`. Guessing would turn that into a real post to every enabled platform. Refusing turns it into a message.

## Commands

| Command | What it does |
| ------- | ------------ |
| `crier` / `crier publish` | render, then post to every enabled platform |
| `crier render` | render and write the file; no network |
| `crier init` | write a configuration file to start from |
| `crier ping` | check every enabled platform's credentials, without posting |
| `crier platforms` | which platforms are enabled, and which are configured |
| `crier config` | the resolved configuration, secrets redacted |
| `crier self-update` | replace this binary with the newest release |

Results go to standard output and logs go to standard error. This lets you pipe a result while the logs stay readable. The [exit code](./exit-codes.md) tells you what happened.

## Global flags

| Flag | What it does |
| ---- | ------------ |
| `--version` | print the version, the commit, the build date and the Go version, then exit 0 |
| `--version --json` | the same as a JSON object |
| `--help` | the top-level usage |
| `--set key=value` | set any configuration key by its dotted name; repeatable |

Every configuration key is also a flag on the commands that read configuration: `--render-width`, `--publish-telegram-chat-id`, and so on. See [configuration](../configuration/README.md) and `crier render -h` for the list.

## `crier init`

This command writes a configuration file. Run it first in a new directory. It is the only command that works without an existing configuration.

```sh
crier init                 # a short commented crier.yaml, ready to edit
crier init --full          # every option crier has, with its default
crier init --format toml   # crier.toml instead
```

| Flag | Default | What it does |
| ---- | ------- | ------------ |
| `--full` | off | write every option with its default, rather than a starter |
| `--format` | `yaml` | `yaml`, `json` or `toml` |
| `--output` | `crier.<format>` here | write somewhere else |
| `--force` | off | overwrite a file that is already there |

Without `--force`, the command leaves an existing file alone and exits 1. A config is something somebody writes by hand. A generator that overwrites it is a generator nobody runs twice.

The starter names a template and a data file. It sets a size and stubs one platform with `enabled: false` so nothing posts by accident. The written path goes to standard output. The next steps go to standard error.

The `--full` output has the same content as [`crier.example.yaml`](../../crier.example.yaml) in the repository. Both are generated from the same walk over the registry from inside the binary. This means what `init` writes cannot drift from what crier reads.

### What to do next

```sh
crier render        # see the picture
crier --dry-run     # see what would be posted, without posting it
crier               # post it
```

## `crier ping`

This command asks every enabled platform who the credentials belong to. It posts nothing.

```sh
crier ping
```

```
TARGET     STATUS  ACCOUNT              MS   DETAIL
discord    ok      crier hook (d-1)     91   channel 55
mastodon   ok      @crier@example.social 140
telegram   ok      @crier_bot (42)      88
stage:s3   ok      bucket media at s3.example.com  63
```

This answers "is this set up right" without making you post a test message. Every call it makes is a read against the cheapest identity endpoint the platform offers:

| Platform | What it calls |
| -------- | ------------- |
| Instagram | `GET /{user-id}?fields=id,username` |
| Facebook | `GET /{page-id}?fields=id,name` |
| TikTok | `POST /v2/post/publish/creator_info/query/` |
| Telegram | `getMe` |
| X | `GET /2/users/me` |
| Mastodon | `GET /api/v1/accounts/verify_credentials` |
| Discord | `GET` on the webhook URL |
| LinkedIn | `GET /v2/userinfo` |
| Reddit | a token grant, then `GET /api/v1/me` |

When staging holds a credential, such as `stage.mode: s3`, the bucket is checked too. It uses a HEAD request for this. The other modes have nothing to check. The `none` mode does nothing. The `url` mode is a string you vouched for. The `server` mode binds a socket instead of holding a key. These modes get no row in the output, rather than a row saying "ok".

There is no `--dry-run`. The ping command *is* the safe check. A dry ping would check nothing at all.

### Exit codes

| Code | Meaning |
| ---- | ------- |
| 0 | every credential was accepted |
| 4 | some were, some were not; the table names which |
| 5 | none were |
| 1 | nothing is enabled, or the configuration is wrong |

### LinkedIn is a special case

Posting needs the `w_member_social` scope. Every endpoint that can name the account needs something else. The `/v2/me` endpoint needs `r_liteprofile`. The `/v2/userinfo` endpoint needs `openid` and `profile`. Because of this, a perfectly good posting token might not be able to say who it belongs to.

crier tells the two refusals apart. A 401 means the token is not valid, and the row fails. A 403 means the token is valid but cannot read a profile. When this happens, the row passes and shows the configured `author-urn`. It also notes that adding the `openid` and `profile` scopes would let ping report the name as well.

## `crier render`

This command renders and writes a file. It never touches the network. This is the loop to work in while a template takes shape. See [rendering](../rendering/README.md).

## `crier publish`

This is the default. It renders once per distinct shape. It stages the file when a platform needs a URL to fetch, and it fans out. Using `--dry-run` does everything except the requests that would post. See [publishing](../publishing/README.md).

You can also skip the rendering entirely. Using `--publish-input card.png` posts a file that already exists. Using `render.video.frames-input` encodes frames made elsewhere. See [flows](../publishing/flows.md).

## `crier self-update`

This command replaces your current binary with one downloaded from a crier release. It verifies the download against GitHub's digest. It also runs the new binary once before anything is moved. Use `--rollback` to put back what the last update replaced. See [installing](./install.md#keeping-it-up-to-date).

## `crier platforms` and `crier config`

The `platforms` command answers "why did nothing post". It lists every platform. It tells you whether each one is enabled and whether what it needs is set.

The `config` command answers "which value won". It prints the resolved configuration and the file it came from. It redacts secrets. Add `--all` to include the keys left at their defaults. Add `--json` to make it machine-readable.
