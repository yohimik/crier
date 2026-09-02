# Configuration

You can provide every crier setting in three places. They apply in this order:

1. **a configuration file**: YAML, JSON or TOML. Crier finds this by walking up from the working directory.
2. **an environment variable**: `CRIER_` plus the key. Dots and dashes turn into underscores.
3. **a command line flag**: the key with dots turned into dashes.

A later layer wins over an earlier one. If a layer says nothing about a key, the earlier value stays.

You can also use `--set key=value`. You can repeat this flag. It sets any key by its dotted name. This belongs to the flag layer. It outranks a key's own flag because it is more specific.

```yaml
# crier.yaml
render:
  width: 1080
```

```sh
CRIER_RENDER_WIDTH=1200 crier render          # 1200, the environment wins
crier render --render-width 900               # 900, the flag wins
crier render --set render.width=800           # 800, --set wins
```

Using `--set` with a key crier does not have is an error. The value does not just go nowhere:

```
$ crier config --set render.widht=900
crier: --set: unknown key "render.widht"; did you mean render.width?
```

Every key is in the [reference](./reference/README.md). Every key with its default is in [`crier.example.yaml`](../../crier.example.yaml).

## Getting a file to start from

```sh
crier init            # a short commented crier.yaml
crier init --full     # every option, with its default
```

`--full` writes the same content as `crier.example.yaml`. It uses the same walk over the registry inside the binary. This ensures it cannot fall behind the keys crier actually reads. `--format json` and `--format toml` write the other two spellings crier accepts. See [the command line](../operations/cli.md).

## The three formats

A configuration file can use YAML, JSON, or TOML. The file extension determines the format. All three are equivalent. They share the same keys, the same nesting, and the same resulting configuration. Nothing crier does depends on which one you pick.

```yaml
# crier.yaml
render:
  width: 1080
publish:
  telegram:
    chat-id: "@my_channel"
```

```json
{ "render": { "width": 1080 },
  "publish": { "telegram": { "chat-id": "@my_channel" } } }
```

```toml
[render]
width = 1080

[publish.telegram]
chat-id = "@my_channel"
```

The `crier init --format yaml|json|toml` command writes any of the three formats. The `crier config` command resolves all three to the same values. An end-to-end test asserts exactly that.

## Finding the file

crier looks for `crier.yaml`, `crier.yml`, `crier.json`, `crier.toml` or `.crier.yaml` in the working directory. It then checks the parent directory. It repeats this up to the filesystem root. This is how git finds a repository. The nearest file wins.

Within a single directory, that list is the order of preference. A directory holding both a `crier.yaml` and a `crier.json` uses the YAML one.

This lets one machine hold several projects:

```
~/work/product-a/crier.yaml     # a's template, a's platforms
~/work/product-b/crier.yaml     # b's
```

The command `cd product-a && crier` uses a's configuration from anywhere inside `product-a`. File discovery and the default command make that one line the whole flow. Using `--config path/to/file` or `CRIER_CONFIG` skips the search.

A found file that cannot be parsed is an error. crier does not step over it. Running with somebody else's settings is worse than not running.

## Where relative paths point

This part is worth reading twice.

- A relative path **written in the configuration file** resolves against **that file's directory**. A project keeps its template next to its config. It writes `template: card.html`. Then `crier` finds it from any subdirectory.
- A relative path given as a **flag or an environment variable** resolves against the **working directory**. This is because you typed it there.

This applies to keys marked `path` in the reference:
`render.template`, `render.data`, `render.css`, `render.overlays`,
`render.pool`, `render.output`, `render.fonts-dir`, `render.base-url`,
`render.video.audio` and each `publish.<platform>.overlay`.

## Formats, and `$ref`

JSON, YAML and TOML all work. A file can also pull in another:

```yaml
log:
  $ref: shared/logging.yaml
```

Keys match case-insensitively. If crier does not know a key, it throws an error with the full path. A typo that loads is configuration that silently never applies.

## Seeing what will be used

```sh
crier config            # what differs from the defaults, secrets redacted
crier config --all      # every key
crier config --json     # the same, for a script
```
## Secrets

Keys marked **Secret** in the reference are redacted by `crier config`. They are never logged. Keep them out of the file that lives in version control. Pass them as environment variables instead:

```sh
export CRIER_PUBLISH_TELEGRAM_TOKEN="…"
```
