# Tunnels

`stage.mode: server` serves the file from this machine. A laptop has no public
address, so Instagram's fetcher cannot reach it. A tunnel gives it one.

crier spawns the tunnel as a subprocess, waits for it to report its URL, and
kills it — and its children — when the run ends.

The tunnel program itself is a prerequisite crier does not bundle.

## ngrok

```yaml
stage:
  mode: server
  server:
    listen: 127.0.0.1:0
    tunnel:
      mode: ngrok
```

crier runs `ngrok http <port> --log stdout --log-format json` and reads the
public URL from ngrok's local agent API at `http://127.0.0.1:4040/api/tunnels`,
falling back to scanning the output. The API is the more reliable of the two:
the log format has changed before, and the API has not.

**Free-tier ngrok shows an interstitial page** to first-time visitors. Meta's
fetcher receives that page instead of the image, and the post fails with a
`status_code: ERROR` that says nothing about why. A paid ngrok plan removes it;
zrok does not have it.

## zrok

```yaml
stage:
  server:
    tunnel:
      mode: zrok
```

`zrok share public http://<addr> --headless`, with the URL read from its
output. No interstitial, which makes it the better free option for Instagram.

## custom

Any program that prints a URL:

```yaml
stage:
  server:
    tunnel:
      mode: custom
      bin: cloudflared
      args: ["tunnel", "--url", "http://{addr}"]
      url-pattern: 'https://[a-z0-9-]+\.trycloudflare\.com'
```

`{port}` and `{addr}` are substituted into the arguments. `url-pattern` is a
regular expression with **exactly one capture group**, applied to the combined
output — crier refuses a pattern with any other number of groups rather than
guessing which part is the URL.

## Lifecycle

- `stage.server.tunnel.startup-timeout` (30s) is how long crier waits for a
  URL. Timing out is exit code 6, with the program's own output attached.
- A tunnel that exits before reporting a URL fails immediately rather than
  waiting out the timeout.
- The tunnel's output goes to the log at debug level; the URL is logged at
  info.
- On shutdown crier signals the whole process group, then kills it — ngrok and
  zrok both spawn children, and signalling only the parent leaves them holding
  the port.

## Not both

Setting `stage.server.public-url` **and** a tunnel mode is a configuration
error. A tunnel discovers the URL itself; saying it twice means one of the two
answers is wrong and crier cannot tell which.

Configuration keys: [`stage.server.tunnel.*`](../configuration/reference/stage-server-tunnel.md).
