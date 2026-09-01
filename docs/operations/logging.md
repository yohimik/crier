# Logging

Logs go to **standard error**. Results go to **standard output**. That split is
what lets a script read one without filtering the other:

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

`--log-level debug` is the first thing to reach for when a publish fails: it
shows every request, its status, and every retry.

`--log-level warn` is the first thing to reach for when a template renders
wrongly: unsupported CSS is reported there.

## Formats

```yaml
log:
  format: console    # or json
```

`console` is human-readable and coloured when the terminal supports it. `json`
is one object per line:

```json
{"level":"info","platform":"telegram","id":"1001","url":"https://t.me/…","time":"2026-09-01T07:05:36+04:00","message":"published"}
```

## webrender's own logging

The layout engine logs to standard output by default, which is where crier's
results go. crier redirects it: progress becomes `trace` and warnings become
`warn`, both tagged `from=webrender`. Nothing it prints can corrupt a report.

Configuration keys: [`log.*`](../configuration/reference/log.md).
