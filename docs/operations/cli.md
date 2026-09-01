# The command line

crier is one binary with a handful of commands. Publishing is what it does with
no command at all, so the everyday invocation has no words in it:

```sh
cd my-project && crier
```

## Dispatch

The rule crier applies to its first argument:

| First argument | What runs |
| -------------- | --------- |
| nothing | `publish` |
| something starting with `-` | `publish`, with that flag |
| `--version` or `-version` | the version, then exit |
| `-h`, `--help` | the top-level usage |
| a known command word | that command |
| an unknown bare word | a usage error, exit 2 |

`--version` is special-cased ahead of the leading-dash rule: without that, it
would be handed to `publish` as a flag `publish` has never heard of.

## Commands

| Command | What it does |
| ------- | ------------ |
| `crier` / `crier publish` | render, then post to every enabled platform |
| `crier render` | render and write the file; no network |
| `crier init` | write a configuration file to start from |
| `crier ping` | check every enabled platform's credentials, without posting |
| `crier platforms` | which platforms are enabled, and which are configured |
| `crier config` | the resolved configuration, secrets redacted |

Results go to standard output and logs to standard error, so a result can be
piped while the logs stay readable. The
[exit code](./exit-codes.md) says what happened.

## Global flags

| Flag | What it does |
| ---- | ------------ |
| `--version` | print the version, the commit, the build date and the Go version, then exit 0 |
| `--version --json` | the same as a JSON object |
| `--help` | the top-level usage |

Every configuration key is also a flag on the commands that read configuration
— `--render-width`, `--publish-telegram-chat-id`, and so on. See
[configuration](../configuration/README.md), and `crier render -h` for the
list.

## `crier init`

Writes a configuration file. It is the first thing to run in a new directory,
and the only command that does not need a configuration to already exist.

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

Without `--force`, an existing file is left alone and the command exits 1: a
config is something somebody wrote by hand, and a generator that overwrites it
is a generator nobody runs twice.

The starter names a template and a data file, sets a size, and stubs one
platform with `enabled: false`, so nothing posts by accident. The path it wrote
goes to standard output; the next steps go to standard error.

`--full` is the same content as
[`crier.example.yaml`](../../crier.example.yaml) in the repository — both are
generated from the same walk over the registry, from inside the binary, so what
`init` writes cannot drift from what crier reads.

### What to do next

```sh
crier render        # see the picture
crier --dry-run     # see what would be posted, without posting it
crier               # post it
```

## `crier ping`

Asks every enabled platform who the credentials belong to, and posts nothing.

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

It is the answer to "is this set up right" that is not "post something and
see". Every call it makes is a read against the cheapest identity endpoint the
platform offers:

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

When staging holds a credential — `stage.mode: s3` — the bucket is checked too,
with a HEAD. The other modes have nothing to check: `none` does nothing, `url`
is a string you vouched for, and `server` binds a socket rather than holding a
key, so they get no row rather than a row saying "ok".

There is no `--dry-run`. ping *is* the safe check, and a dry ping would check
nothing at all.

### Exit codes

| Code | Meaning |
| ---- | ------- |
| 0 | every credential was accepted |
| 4 | some were, some were not — the table names which |
| 5 | none were |
| 1 | nothing is enabled, or the configuration is wrong |

### LinkedIn is a special case

Posting needs `w_member_social`; every endpoint that could name the account
needs something else — `/v2/me` needs `r_liteprofile`, `/v2/userinfo` needs
`openid` and `profile`. So a perfectly good posting token may be unable to say
who it belongs to.

crier tells the two refusals apart. A 401 means the token is not valid and the
row fails. A 403 means the token is valid and merely cannot read a profile: the
row passes, shows the configured `author-urn`, and notes that adding the
`openid` and `profile` scopes would let ping report the name as well.

## `crier render`

Renders and writes a file, and never touches the network. It is the loop to
work in while a template is taking shape. See
[rendering](../rendering/README.md).

## `crier publish`

The default. Renders once per distinct shape, stages the file when a platform
needs a URL to fetch, and fans out. `--dry-run` does everything except the
requests that would post. See [publishing](../publishing/README.md).

## `crier platforms` and `crier config`

`platforms` answers "why did nothing post": it lists every platform, whether it
is enabled, and whether what it needs is set.

`config` answers "which value won": it prints the resolved configuration with
the file it came from, secrets redacted. `--all` includes the keys left at
their defaults, and `--json` makes it machine-readable.
