# Exit codes

Each code names a category rather than a particular failure. This lets your script branch on the category.

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

The most important distinction is **4 against 5**. A 4 means some platforms already have the post. Re-running the whole thing would double-post to those. A 5 means nothing went out. This means re-running is safe.

`crier ping` reuses the same three codes for the same reason. A 0 means every credential was accepted. A 4 means some were accepted and some were not. A 5 means none were accepted.

## Why a failure is a 1 rather than a 5

Everything crier finds before sending anything is a configuration error. Examples include a missing token, a caption that will not render, a video routed at a platform that cannot take one, or an Instagram post with no staging. These errors cost no uploads. They are worth failing early.
