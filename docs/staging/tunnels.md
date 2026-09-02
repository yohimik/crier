# Tunnels

`stage.mode: server` serves the file from this machine. A laptop has no public address, so Instagram's fetcher cannot reach it. A tunnel gives it one.

crier spawns the tunnel as a subprocess. It waits for the tunnel to report its URL. When the run ends, crier kills the tunnel and its children.

The tunnel program itself is a prerequisite. crier does not bundle it.

## ngrok

```yaml
stage:
  mode: server
  server:
    listen: 127.0.0.1:0
    tunnel:
      mode: ngrok
```

crier runs `ngrok http <port> --log stdout --log-format json`. It reads the public URL from ngrok's local agent API at `http://127.0.0.1:4040/api/tunnels`. It falls back to scanning the output. The API is more reliable. The log format has changed before, but the API has not.

**Free-tier ngrok shows an interstitial page** to first-time visitors. Meta's fetcher receives that page instead of the image. The post fails with a `status_code: ERROR`. This error says nothing about why. A paid ngrok plan removes it. zrok does not have it.

## zrok

```yaml
stage:
  server:
    tunnel:
      mode: zrok
```

Run `zrok share public http://<addr> --headless`. Read the URL from its output. It has no interstitial. This makes it the better free option for Instagram.

## custom

You can use any program that prints a URL:

```yaml
stage:
  server:
    tunnel:
      mode: custom
      bin: cloudflared
      args: ["tunnel", "--url", "http://{addr}"]
      url-pattern: 'https://[a-z0-9-]+\.trycloudflare\.com'
```

The tool substitutes `{port}` and `{addr}` into the arguments. The `url-pattern` is a regular expression applied to the combined output. It must have **exactly one capture group**. The crier tool refuses a pattern with any other number of groups instead of guessing which part is the URL.

## Lifecycle

- The `stage.server.tunnel.startup-timeout` setting (30s) is how long crier waits for a URL. A timeout results in exit code 6 and attaches the program's own output.
- If a tunnel exits before reporting a URL, it fails immediately. It does not wait for the timeout.
- The tunnel's output goes to the log at the debug level. The URL is logged at the info level.
- On shutdown, crier signals the whole process group and then kills it. Both ngrok and zrok spawn children. Signalling only the parent leaves them holding the port.

## Not both

Setting `stage.server.public-url` **and** a tunnel mode is a configuration error. A tunnel discovers the URL itself. If you provide it twice, one of the two answers is wrong. This means crier cannot tell which one to use.

Configuration keys: [`stage.server.tunnel.*`](../configuration/reference/stage-server-tunnel.md).
