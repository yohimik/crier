# Exit codes

Each code names a category a script can branch on, rather than a particular
failure.

| Code | Name | When |
| ---- | ---- | ---- |
| 0 | ok | everything worked |
| 1 | config error | a value is wrong or missing, a platform is not configured, a platform needs a URL and nothing can produce one |
| 2 | usage error | the command line was wrong: an unknown command, an unknown flag |
| 3 | render error | the template would not parse or execute, an overlay is broken, the layout produced more than one page, a caption template failed, ffmpeg failed |
| 4 | partial publish failure | some platforms took the post and others did not |
| 5 | publish failure | no platform took the post |
| 6 | staging error | the file could not be made reachable: an S3 upload failed, a tunnel never reported a URL, the listener could not start |

## Branching on them

```sh
crier
case $? in
  0) echo "posted everywhere" ;;
  4) echo "posted somewhere; see the report" ;;   # often worth retrying the rest by hand
  1|2|3) exit 1 ;;                                # a mistake, not a transient failure
  5|6) exit 1 ;;                                  # nothing went out
esac
```

The distinction that matters most is **4 against 5**. A 4 means some platforms
already have the post: re-running the whole thing would double-post to those.
A 5 means nothing went out and re-running is safe.

## Why a failure is a 1 rather than a 5

Everything crier can find out before sending anything is a configuration error:
a missing token, a caption that will not render, a video routed at a platform
that cannot take one, an Instagram post with no staging. Those cost no uploads
and are worth failing early.
