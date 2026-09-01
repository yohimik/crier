package stage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/httpx"
)

// The tunnel tests run this test binary as a stand-in for ngrok or zrok.
const (
	fakeTunnelEnv     = "CRIER_FAKE_TUNNEL"
	fakeTunnelURL     = "CRIER_FAKE_TUNNEL_URL"
	fakeTunnelSilence = "CRIER_FAKE_TUNNEL_SILENT"
	fakeTunnelExit    = "CRIER_FAKE_TUNNEL_EXIT"
)

func TestMain(m *testing.M) {
	if os.Getenv(fakeTunnelEnv) != "" {
		fakeTunnelMain()
		return
	}
	os.Exit(m.Run())
}

func fakeTunnelMain() {
	if os.Getenv(fakeTunnelExit) != "" {
		fmt.Fprintln(os.Stderr, "the tunnel could not start")
		os.Exit(1)
	}
	if os.Getenv(fakeTunnelSilence) == "" {
		url := os.Getenv(fakeTunnelURL)
		if url == "" {
			url = "https://fake.example"
		}
		fmt.Fprintf(os.Stdout, "listening url=%s for %v\n", url, os.Args[1:])
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}

func testLogger(t *testing.T) zerolog.Logger {
	return zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.DebugLevel)
}

func writeAsset(t *testing.T, body string) Asset {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "card.png")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Asset{Path: p, ContentType: "image/png", Name: "card.png", Size: int64(len(body))}
}

// --- none and url ----------------------------------------------------------

func TestNoneRefusesWithAnActionableError(t *testing.T) {
	s := None{}
	if s.Name() != "none" {
		t.Errorf("name = %q", s.Name())
	}
	_, err := s.Stage(context.Background(), Asset{})
	if !errors.Is(err, ErrNoStaging) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "stage.mode") {
		t.Errorf("the error should name the key to set, got %v", err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Error(err)
	}
}

func TestURLStager(t *testing.T) {
	s := NewURL("https://cdn.example/card.png")
	if s.Name() != "url" {
		t.Errorf("name = %q", s.Name())
	}
	obj, err := s.Stage(context.Background(), Asset{})
	if err != nil {
		t.Fatal(err)
	}
	if obj.URL != "https://cdn.example/card.png" {
		t.Errorf("url = %q", obj.URL)
	}
	if err := obj.Remove(context.Background()); err != nil {
		t.Errorf("removing a file crier did not upload should do nothing: %v", err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Error(err)
	}

	if _, err := NewURL("").Stage(context.Background(), Asset{}); err == nil {
		t.Error("an empty URL should be refused")
	}
}

func TestNewChoosesTheMode(t *testing.T) {
	for _, tt := range []struct {
		mode string
		want string
	}{
		{"", "none"},
		{"none", "none"},
		{"NONE", "none"},
		{"url", "url"},
	} {
		s, err := New(Options{Config: config.Stage{Mode: tt.mode, URL: "https://x.example/a"}})
		if err != nil {
			t.Fatalf("mode %q: %v", tt.mode, err)
		}
		if s.Name() != tt.want {
			t.Errorf("mode %q gave %q", tt.mode, s.Name())
		}
	}
	if _, err := New(Options{Config: config.Stage{Mode: "carrier-pigeon"}}); err == nil {
		t.Error("an unknown mode should be refused")
	}
}

func TestNewServerMode(t *testing.T) {
	s, err := New(Options{
		Config: config.Stage{Mode: "server", Server: config.Server{Listen: "127.0.0.1:0", PublicURL: "https://x.example"}},
		Dir:    t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Name() != "server" {
		t.Errorf("name = %q", s.Name())
	}
	if err := s.Close(context.Background()); err != nil {
		t.Error(err)
	}
}

func TestNewS3ModeNeedsAnEndpoint(t *testing.T) {
	if _, err := New(Options{Config: config.Stage{Mode: "s3"}}); err == nil {
		t.Error("an S3 stager with no endpoint should be refused")
	}
}

// --- local server ----------------------------------------------------------

func newServer(t *testing.T, cfg config.Server) *Server {
	t.Helper()
	s, err := NewServer(cfg, testLogger(t), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}

func TestServerServesWhatItStaged(t *testing.T) {
	s := newServer(t, config.Server{Listen: "127.0.0.1:0", PublicURL: "http://placeholder"})
	obj, err := s.Stage(context.Background(), writeAsset(t, "PNGDATA"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(obj.URL, "http://placeholder/") {
		t.Fatalf("url = %q, want the configured public base", obj.URL)
	}

	// The public base is a placeholder, so the fetch goes to the real listener.
	local := "http://" + s.Addr() + obj.URL[len("http://placeholder"):]
	body, status := get(t, local)
	if status != http.StatusOK || body != "PNGDATA" {
		t.Fatalf("status=%d body=%q", status, body)
	}

	if _, status := get(t, "http://"+s.Addr()+"/not-staged"); status != http.StatusNotFound {
		t.Errorf("an unknown path should be a 404, got %d", status)
	}
	if _, status := get(t, "http://"+s.Addr()+"/../../etc/passwd"); status == http.StatusOK {
		t.Error("only staged files may be served")
	}

	if err := obj.Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, status := get(t, local); status != http.StatusNotFound {
		t.Errorf("a removed file should be a 404, got %d", status)
	}
	// Removing twice is harmless.
	if err := obj.Remove(context.Background()); err != nil {
		t.Errorf("second remove: %v", err)
	}
}

func TestServerRejectsOtherMethods(t *testing.T) {
	s := newServer(t, config.Server{Listen: "127.0.0.1:0", PublicURL: "http://placeholder"})
	if _, err := s.Stage(context.Background(), writeAsset(t, "x")); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://"+s.Addr()+"/whatever", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestServerReleasesThePortOnClose(t *testing.T) {
	s := newServer(t, config.Server{Listen: "127.0.0.1:0", PublicURL: "http://placeholder"})
	if _, err := s.Stage(context.Background(), writeAsset(t, "x")); err != nil {
		t.Fatal(err)
	}
	addr := s.Addr()
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The port has to be free again: a run that leaks it makes the next run
	// fail for a reason nobody can see.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("the port was not released: %v", err)
	}
	_ = ln.Close()
}

func TestServerRemovesItsTemporaryDirectory(t *testing.T) {
	s, err := NewServer(config.Server{Listen: "127.0.0.1:0", PublicURL: "http://x"}, testLogger(t), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Stage(context.Background(), writeAsset(t, "x")); err != nil {
		t.Fatal(err)
	}
	dir := s.dir
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the staging directory survived Close: %v", err)
	}
}

func TestServerKeepsACallersDirectory(t *testing.T) {
	dir := t.TempDir()
	s, err := NewServer(config.Server{Listen: "127.0.0.1:0", PublicURL: "http://x"}, testLogger(t), nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Stage(context.Background(), writeAsset(t, "x")); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("a directory crier did not create should survive: %v", err)
	}
}

func TestServerWithoutAPublicURLOrTunnelIsRefused(t *testing.T) {
	s := newServer(t, config.Server{Listen: "127.0.0.1:0"})
	_, err := s.Stage(context.Background(), writeAsset(t, "x"))
	if err == nil || !strings.Contains(err.Error(), "stage.server.public-url") {
		t.Fatalf("err = %v", err)
	}
	// The failure is remembered rather than retried on every asset.
	if _, again := s.Stage(context.Background(), writeAsset(t, "x")); again == nil {
		t.Error("the second attempt should fail the same way")
	}
}

func TestServerStagesTwoFilesAtDistinctURLs(t *testing.T) {
	s := newServer(t, config.Server{Listen: "127.0.0.1:0", PublicURL: "http://placeholder"})
	a, err := s.Stage(context.Background(), writeAsset(t, "one"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Stage(context.Background(), writeAsset(t, "two"))
	if err != nil {
		t.Fatal(err)
	}
	if a.URL == b.URL {
		t.Fatal("two assets must not share a URL")
	}
}

func TestServerRefusesAMissingFile(t *testing.T) {
	s := newServer(t, config.Server{Listen: "127.0.0.1:0", PublicURL: "http://placeholder"})
	_, err := s.Stage(context.Background(), Asset{Path: filepath.Join(t.TempDir(), "gone.png")})
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestServerBaseURLAndAddrBeforeStart(t *testing.T) {
	s := newServer(t, config.Server{Listen: "127.0.0.1:0", PublicURL: "http://x"})
	if s.Addr() != "" || s.BaseURL() != "" {
		t.Error("nothing is listening until the first Stage")
	}
}

func TestServerCannotListen(t *testing.T) {
	// Take a port, then ask the server for the same one.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	s := newServer(t, config.Server{Listen: ln.Addr().String(), PublicURL: "http://x"})
	if _, err := s.Stage(context.Background(), writeAsset(t, "x")); err == nil {
		t.Fatal("expected a listen error")
	}
}

func get(t *testing.T, url string) (string, int) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The traversal test needs the path left as written.
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return string(body), resp.StatusCode
}

func TestCopyFileErrors(t *testing.T) {
	dir := t.TempDir()
	if err := copyFile(filepath.Join(dir, "missing"), filepath.Join(dir, "out")); err == nil {
		t.Error("expected a read error")
	}
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, filepath.Join(dir, "no-such-dir", "out")); err == nil {
		t.Error("expected a write error")
	}
}

func TestSanitiseName(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"card.png", "card.png"},
		{"a b/c.png", "a-b-c.png"},
		{"../../etc/passwd", "..-..-etc-passwd"},
		{"", "asset"},
		{"???", "---"},
	} {
		if got := sanitiseName(tt.in); got != tt.want {
			t.Errorf("sanitiseName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRandomTokenIsRandom(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		tok := randomToken()
		if len(tok) != 16 {
			t.Fatalf("token %q is not 16 hex characters", tok)
		}
		if seen[tok] {
			t.Fatal("a token repeated")
		}
		seen[tok] = true
	}
}

// --- tunnel ----------------------------------------------------------------

func fakeTunnelConfig(mode string, extra ...string) config.Tunnel {
	return config.Tunnel{
		Mode:           mode,
		Bin:            os.Args[0],
		Args:           extra,
		URLPattern:     `url=(https://\S+)`,
		StartupTimeout: "10s",
	}
}

func withFakeTunnelEnv(t *testing.T, extra map[string]string) {
	t.Helper()
	t.Setenv(fakeTunnelEnv, "1")
	for k, v := range extra {
		t.Setenv(k, v)
	}
}

func TestCustomTunnelFindsTheURL(t *testing.T) {
	withFakeTunnelEnv(t, map[string]string{fakeTunnelURL: "https://tunnel.example"})

	tun, err := NewTunnel(TunnelOptions{Config: fakeTunnelConfig("custom", "--addr", "{addr}", "--port", "{port}"), Logger: testLogger(t)})
	if err != nil {
		t.Fatal(err)
	}
	url, err := tun.Start(context.Background(), "127.0.0.1:4321")
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://tunnel.example" {
		t.Errorf("url = %q", url)
	}
	if err := tun.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Stopping twice is harmless.
	if err := tun.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTunnelSubstitutesPlaceholders(t *testing.T) {
	p, err := presetFor(TunnelCustom, config.Tunnel{
		Bin: "x", URLPattern: "(https://\\S+)", Args: []string{"--addr={addr}", "--port={port}", "plain"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := p.args("8080", "127.0.0.1:8080")
	want := []string{"--addr=127.0.0.1:8080", "--port=8080", "plain"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestTunnelStartupTimeout(t *testing.T) {
	withFakeTunnelEnv(t, map[string]string{fakeTunnelSilence: "1"})

	cfg := fakeTunnelConfig("custom")
	cfg.StartupTimeout = "200ms"
	tun, err := NewTunnel(TunnelOptions{Config: cfg, Logger: testLogger(t)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tun.Start(context.Background(), "127.0.0.1:4321")
	if err == nil {
		t.Fatal("expected a timeout")
	}
	if !strings.Contains(err.Error(), "did not report a public URL") {
		t.Errorf("err = %v", err)
	}
}

func TestTunnelThatExitsImmediately(t *testing.T) {
	withFakeTunnelEnv(t, map[string]string{fakeTunnelExit: "1"})

	tun, err := NewTunnel(TunnelOptions{Config: fakeTunnelConfig("custom"), Logger: testLogger(t)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tun.Start(context.Background(), "127.0.0.1:4321")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "the tunnel could not start") {
		t.Errorf("the program's own output should be attached, got %v", err)
	}
}

func TestNgrokTunnelReadsTheAgentAPI(t *testing.T) {
	api := newAgentAPI(t, "https://ngrok.example")
	defer api.Close()

	withFakeTunnelEnv(t, map[string]string{fakeTunnelSilence: "1"})
	cfg := fakeTunnelConfig("ngrok")
	cfg.APIURL = api.URL
	cfg.URLPattern = ""

	tun, err := NewTunnel(TunnelOptions{
		Config: cfg,
		Logger: testLogger(t),
		Client: httpx.New(httpx.Options{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	url, err := tun.Start(context.Background(), "127.0.0.1:4321")
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://ngrok.example" {
		t.Errorf("url = %q", url)
	}
	if err := tun.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func newAgentAPI(t *testing.T, url string) *httpTestServer {
	t.Helper()
	return newHTTPTestServer(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"tunnels":[{"public_url":"http://insecure.example","proto":"http"},`+
			`{"public_url":%q,"proto":"https"}]}`, url)
	})
}

func TestTunnelModeErrors(t *testing.T) {
	if _, err := NewTunnel(TunnelOptions{Config: config.Tunnel{Mode: "wormhole"}}); err == nil {
		t.Error("an unknown mode should be refused")
	}
	if _, err := NewTunnel(TunnelOptions{Config: config.Tunnel{Mode: "custom"}}); err == nil {
		t.Error("custom mode needs a binary")
	}
	if _, err := NewTunnel(TunnelOptions{Config: config.Tunnel{Mode: "custom", Bin: "x", URLPattern: "("}}); err == nil {
		t.Error("a broken pattern should be refused")
	}
	if _, err := NewTunnel(TunnelOptions{Config: config.Tunnel{Mode: "custom", Bin: "x", URLPattern: `https://\S+`}}); err == nil {
		t.Error("a pattern with no capture group should be refused")
	}
}

func TestTunnelPresetDefaults(t *testing.T) {
	p, err := presetFor(TunnelNgrok, config.Tunnel{Args: []string{"--region", "eu"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.bin != "ngrok" {
		t.Errorf("bin = %q", p.bin)
	}
	args := strings.Join(p.args("9000", "127.0.0.1:9000"), " ")
	if !strings.Contains(args, "http 9000") || !strings.Contains(args, "--log-format json") ||
		!strings.Contains(args, "--region eu") {
		t.Errorf("args = %q", args)
	}

	p, err = presetFor(TunnelZrok, config.Tunnel{})
	if err != nil {
		t.Fatal(err)
	}
	if p.bin != "zrok" {
		t.Errorf("bin = %q", p.bin)
	}
	args = strings.Join(p.args("9000", "127.0.0.1:9000"), " ")
	if !strings.Contains(args, "share public http://127.0.0.1:9000 --headless") {
		t.Errorf("args = %q", args)
	}
}

func TestTunnelURLPatterns(t *testing.T) {
	if m := ngrokURL.FindStringSubmatch(`{"lvl":"info","url=https://a.ngrok.app","addr":"x"}`); len(m) < 2 {
		t.Error("the ngrok pattern should match its log line")
	}
	line := "access your zrok share at the following endpoint: https://abc123.share.zrok.io"
	if m := zrokURL.FindStringSubmatch(line); len(m) < 2 || m[1] != "https://abc123.share.zrok.io" {
		t.Errorf("the zrok pattern gave %v", zrokURL.FindStringSubmatch(line))
	}
}

func TestPortOf(t *testing.T) {
	if got, err := portOf("127.0.0.1:8080"); err != nil || got != "8080" {
		t.Errorf("got %q %v", got, err)
	}
	if _, err := portOf("nonsense"); err == nil {
		t.Error("expected an error")
	}
}

func TestServerWithATunnel(t *testing.T) {
	withFakeTunnelEnv(t, map[string]string{fakeTunnelURL: "https://through-the-tunnel.example"})

	s := newServer(t, config.Server{
		Listen: "127.0.0.1:0",
		Tunnel: fakeTunnelConfig("custom"),
	})
	obj, err := s.Stage(context.Background(), writeAsset(t, "PNGDATA"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(obj.URL, "https://through-the-tunnel.example/") {
		t.Fatalf("url = %q, want the tunnel's own host", obj.URL)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
