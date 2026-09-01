package stage

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/httpx"
	"github.com/yohimik/crier/internal/procutil"
)

// TunnelMode names a way of exposing the local staging server.
type TunnelMode string

const (
	// TunnelNone runs no tunnel; the operator says what the public URL is.
	TunnelNone TunnelMode = "none"
	// TunnelNgrok runs ngrok and reads the URL from its local agent API.
	TunnelNgrok TunnelMode = "ngrok"
	// TunnelZrok runs zrok and reads the URL from its output.
	TunnelZrok TunnelMode = "zrok"
	// TunnelCustom runs a program the operator names, and finds the URL with a
	// pattern the operator writes.
	TunnelCustom TunnelMode = "custom"
)

// Tunnel exposes a local address on a public URL.
type Tunnel interface {
	// Start launches the tunnel and returns the public URL it established.
	Start(ctx context.Context, localAddr string) (string, error)
	// Stop tears the tunnel down.
	Stop(ctx context.Context) error
}

// TunnelOptions configures NewTunnel.
type TunnelOptions struct {
	Config config.Tunnel
	Logger zerolog.Logger
	// Client polls ngrok's local agent API. A nil client uses a plain one.
	Client *httpx.Client
}

// NewTunnel builds the tunnel the configuration asks for.
//
// All three real modes are the same program: spawn something, watch what it
// prints, find a URL. The preset only decides the arguments and where to look,
// which is why there is one implementation and a table rather than three.
func NewTunnel(o TunnelOptions) (Tunnel, error) {
	mode := TunnelMode(strings.ToLower(strings.TrimSpace(o.Config.Mode)))
	preset, err := presetFor(mode, o.Config)
	if err != nil {
		return nil, err
	}
	return &procTunnel{cfg: o.Config, preset: preset, log: o.Logger, client: o.Client}, nil
}

// preset is what one tunnel program needs: how to invoke it and how to learn
// the URL it produced.
type preset struct {
	bin string
	// args builds the command line for a port and an address.
	args func(port, addr string) []string
	// pattern finds the URL in the program's output.
	pattern *regexp.Regexp
	// apiURL, when set, is polled for the URL instead of, and as well as,
	// watching the output.
	apiURL string
}

// ngrokURL matches the https URL ngrok prints in its JSON log lines.
var ngrokURL = regexp.MustCompile(`url=(https://[^\s"]+)`)

// zrokURL matches the share URL zrok prints.
var zrokURL = regexp.MustCompile(`(https://[a-z0-9]+\.share\.[^\s"']+)`)

func presetFor(mode TunnelMode, cfg config.Tunnel) (preset, error) {
	bin := strings.TrimSpace(cfg.Bin)
	switch mode {
	case TunnelNgrok:
		if bin == "" {
			bin = "ngrok"
		}
		return preset{
			bin: bin,
			args: func(port, _ string) []string {
				return append([]string{"http", port, "--log", "stdout", "--log-format", "json"}, cfg.Args...)
			},
			pattern: ngrokURL,
			apiURL:  cfg.APIURL,
		}, nil
	case TunnelZrok:
		if bin == "" {
			bin = "zrok"
		}
		return preset{
			bin: bin,
			args: func(_, addr string) []string {
				return append([]string{"share", "public", "http://" + addr, "--headless"}, cfg.Args...)
			},
			pattern: zrokURL,
		}, nil
	case TunnelCustom:
		if bin == "" {
			return preset{}, fmt.Errorf("stage.server.tunnel.bin is required in custom mode")
		}
		re, err := regexp.Compile(cfg.URLPattern)
		if err != nil {
			return preset{}, fmt.Errorf("stage.server.tunnel.url-pattern: %w", err)
		}
		if re.NumSubexp() != 1 {
			return preset{}, fmt.Errorf(
				"stage.server.tunnel.url-pattern needs exactly one capture group, holding the URL")
		}
		return preset{
			bin: bin,
			args: func(port, addr string) []string {
				out := make([]string, 0, len(cfg.Args))
				for _, a := range cfg.Args {
					a = strings.ReplaceAll(a, "{port}", port)
					a = strings.ReplaceAll(a, "{addr}", addr)
					out = append(out, a)
				}
				return out
			},
			pattern: re,
		}, nil
	default:
		return preset{}, fmt.Errorf("unknown tunnel mode %q", cfg.Mode)
	}
}

// procTunnel runs a tunnel program as a subprocess.
type procTunnel struct {
	cfg    config.Tunnel
	preset preset
	log    zerolog.Logger
	client *httpx.Client

	proc *procutil.Process

	mu    sync.Mutex
	url   string
	found chan struct{}
	once  sync.Once
}

// Start spawns the program and waits for it to say where it can be reached.
func (t *procTunnel) Start(ctx context.Context, localAddr string) (string, error) {
	port, err := portOf(localAddr)
	if err != nil {
		return "", err
	}
	t.found = make(chan struct{})

	timeout := config.Duration(t.cfg.StartupTimeout)
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	args := t.preset.args(port, localAddr)
	t.log.Info().Str("bin", t.preset.bin).Strs("args", args).Msg("starting the tunnel")

	proc, err := procutil.Start(ctx, procutil.Options{
		Name:   t.preset.bin,
		Bin:    t.preset.bin,
		Args:   args,
		Logger: t.log,
		OnLine: t.scan,
	})
	if err != nil {
		return "", fmt.Errorf("starting the tunnel: %w", err)
	}
	t.proc = proc

	url, err := t.await(ctx, timeout)
	if err != nil {
		_ = proc.Stop(context.WithoutCancel(ctx))
		return "", err
	}
	t.log.Info().Str("url", url).Msg("the tunnel is up")
	return url, nil
}

// await waits for the URL, from the output or from the agent API, and gives up
// with the program's own output attached.
func (t *procTunnel) await(ctx context.Context, timeout time.Duration) (string, error) {
	deadline := time.After(timeout)
	poll := time.NewTicker(300 * time.Millisecond)
	defer poll.Stop()

	for {
		select {
		case <-t.found:
			return t.URL(), nil
		case <-t.proc.Done():
			return "", fmt.Errorf("the tunnel exited before it reported a URL:\n%s", t.proc.Tail())
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline:
			return "", fmt.Errorf(
				"the tunnel did not report a public URL within %s; its output was:\n%s",
				timeout, t.proc.Tail())
		case <-poll.C:
			if url := t.pollAPI(ctx); url != "" {
				t.record(url)
				return url, nil
			}
		}
	}
}

// scan looks for the URL in one line of the program's output.
func (t *procTunnel) scan(line string) {
	if t.preset.pattern == nil {
		return
	}
	if m := t.preset.pattern.FindStringSubmatch(line); len(m) > 1 {
		t.record(m[1])
	}
}

func (t *procTunnel) record(url string) {
	t.mu.Lock()
	if t.url == "" {
		t.url = strings.TrimRight(url, "/")
	}
	t.mu.Unlock()
	t.once.Do(func() { close(t.found) })
}

// URL is the public URL, empty until the tunnel reports one.
func (t *procTunnel) URL() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.url
}

// ngrokTunnels is the shape of ngrok's local agent API response.
type ngrokTunnels struct {
	Tunnels []struct {
		PublicURL string `json:"public_url"`
		Proto     string `json:"proto"`
	} `json:"tunnels"`
}

// pollAPI asks ngrok's local agent where it published the tunnel.
//
// It is the more reliable of the two: ngrok's log format has changed before,
// and the agent API has not. Scanning the output stays as the fallback.
func (t *procTunnel) pollAPI(ctx context.Context) string {
	if t.preset.apiURL == "" || t.client == nil {
		return ""
	}
	var out ngrokTunnels
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := t.client.JSON(reqCtx, httpx.NewRequest("GET", t.preset.apiURL), &out); err != nil {
		return ""
	}
	// An https tunnel is what a platform's fetcher will accept.
	for _, tun := range out.Tunnels {
		if strings.HasPrefix(tun.PublicURL, "https://") {
			return tun.PublicURL
		}
	}
	for _, tun := range out.Tunnels {
		if tun.PublicURL != "" {
			return tun.PublicURL
		}
	}
	return ""
}

// Stop tears the tunnel down.
func (t *procTunnel) Stop(ctx context.Context) error {
	if t.proc == nil {
		return nil
	}
	err := t.proc.Stop(ctx)
	t.proc = nil
	if err != nil {
		return fmt.Errorf("stopping the tunnel: %w", err)
	}
	t.log.Debug().Msg("the tunnel is down")
	return nil
}

// portOf is the port half of a listen address.
func portOf(addr string) (string, error) {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("reading the port from %q: %w", addr, err)
	}
	return port, nil
}
