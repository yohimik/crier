# Logging

Logs go to **standard error**. Results go to **standard output**. This setup lets a script read one without filtering the other:

```sh
image=$(crier render)          # the path, and nothing else
crier --json > report.json 2> run.log
```

## Levels

```yaml
log:
  level: info      # trace, debug, info, warn, error
```

| Level | What is at it |
| ----- | ------------- |
| `trace` | webrender's own progress, per-step internals |
| `debug` | HTTP attempts and statuses, subprocess output, render steps, encoded files, staged files, chunk uploads |
| `info` | what a person wants to know: the seed, the template picked from a pool, the tunnel URL, the file rendered, each post published |
| `warn` | soft issues that did not fail the run: a retried request, a cleanup that did not finish, an unsupported CSS property, an Instagram container left behind |
| `error` | a platform that failed, and anything that changes the exit code |

Try `--log-level debug` first when a publish fails. It shows every request, its status, and every retry.

Try `--log-level warn` first when a template renders wrongly. Unsupported CSS is reported there.

## Formats

```yaml
log:
  format: console    # or json
```

The `console` format is human-readable. It is coloured when your terminal supports it. The `json` format prints one object per line:

```json
{"level":"info","platform":"telegram","id":"1001","url":"https://t.me/…","time":"2026-09-01T07:05:36+04:00","message":"published"}
```

## webrender's own logging

By default, the layout engine logs to standard output. This is also where crier sends its results. crier redirects this output. Progress becomes `trace`. Warnings become `warn`. Both get the `from=webrender` tag. Nothing it prints can corrupt a report.

Configuration keys: [`log.*`](../configuration/log.md).
