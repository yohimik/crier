# Configuration

Every crier setting can be given in three places, and they compose in this
order:

1. **a configuration file** — `crier.yaml`, found by walking up from the
   working directory;
2. **an environment variable** — `CRIER_` plus the key, with dots and dashes
   turned into underscores;
3. **a command line flag** — the key with dots turned into dashes.

A later layer wins over an earlier one, and a layer that says nothing about a
key leaves the earlier one alone.

```yaml
# crier.yaml
render:
  width: 1080
```

```sh
CRIER_RENDER_WIDTH=1200 crier render          # 1200, the environment wins
crier render --render-width 900               # 900, the flag wins
```

Every key is in the [reference](./reference/README.md), and every key with its
default is in [`crier.example.yaml`](../../crier.example.yaml).

## Finding the file

crier looks for `crier.yaml`, `crier.yml`, `crier.json`, `crier.toml` or
`.crier.yaml` in the working directory, then in its parent, and so on to the
filesystem root — the way git finds a repository. The nearest one wins.

That is what lets one machine hold several projects:

```
~/work/product-a/crier.yaml     # a's template, a's platforms
~/work/product-b/crier.yaml     # b's
```

`cd product-a && crier` uses a's configuration, from anywhere inside
`product-a` — the discovery and the default command are what make that one
line the whole flow. `--config path/to/file` or `CRIER_CONFIG` skips the search.

A file that is found and cannot be parsed is an error rather than something to
step over: running with somebody else's settings is worse than not running.

## Where relative paths point

This is the part worth reading twice.

- A relative path **written in the configuration file** resolves against **that
  file's directory**. A project keeps its template beside its config, writes
  `template: card.html`, and `crier` finds it from any subdirectory.
- A relative path given as a **flag or an environment variable** resolves
  against the **working directory**, because that is where it was typed.

The keys this applies to are marked `path` in the reference:
`render.template`, `render.data`, `render.css`, `render.overlays`,
`render.pool`, `render.output`, `render.fonts-dir`, `render.base-url`,
`render.video.audio` and each `publish.<platform>.overlay`.

## Formats, and `$ref`

JSON, YAML and TOML all work, and a file can pull in another:

```yaml
log:
  $ref: shared/logging.yaml
```

Keys are matched case-insensitively, and a key crier does not know is an error
naming its full path — a typo that loads is configuration that silently never
applies.

## Seeing what will be used

```sh
crier config            # what differs from the defaults, secrets redacted
crier config --all      # every key
crier config --json     # the same, for a script
```

## Secrets

Keys marked **Secret** in the reference are redacted by `crier config` and
never logged. Keep them out of the file that lives in version control and pass
them as environment variables:

```sh
export CRIER_PUBLISH_TELEGRAM_TOKEN="…"
```
