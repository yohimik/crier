package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yohimik/crier/internal/config"
)

// loads is the check that matters for everything init writes: the loader
// refuses a key it has no field for, so a file that loads is a file whose every
// key crier actually reads.
func loads(t *testing.T, path string) config.Config {
	t.Helper()
	res, err := config.Load(context.Background(), config.Options{Path: path, Environ: []string{}})
	if err != nil {
		t.Fatalf("%s does not load: %v", filepath.Base(path), err)
	}
	return res.Config
}

func TestInitWritesAStarterThatLoads(t *testing.T) {
	dir := t.TempDir()
	code, stdout, stderr := run(t, dir, []string{}, "init")
	if code != ExitOK {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	path := filepath.Join(dir, "crier.yaml")
	if strings.TrimSpace(stdout) != path {
		t.Errorf("stdout = %q, want the path it wrote", stdout)
	}
	cfg := loads(t, path)
	if cfg.Render.Width != 1080 || cfg.Render.Height != 1080 {
		t.Errorf("the starter should set a size: %dx%d", cfg.Render.Width, cfg.Render.Height)
	}
	if cfg.Render.Template == "" || cfg.Render.Data == "" {
		t.Errorf("the starter should name a template and a data file: %+v", cfg.Render)
	}
	if cfg.Publish.Telegram.Enabled {
		t.Error("the starter must not enable a platform: nothing should post by accident")
	}

	// The hint is guidance, not a result, so it belongs on standard error.
	if !strings.Contains(stderr, "crier --dry-run") {
		t.Errorf("no next-steps hint: %q", stderr)
	}
	if !strings.Contains(stderr, "wrote a configuration file") {
		t.Errorf("the write should be logged at info level: %q", stderr)
	}
}

// TestInitFullIsTheWholeRegistry is the anti-drift check on the embedded
// generator: --full and crier.example.yaml come from the same walk, so a key
// added to the registry appears in both or in neither.
func TestInitFullIsTheWholeRegistry(t *testing.T) {
	dir := t.TempDir()
	if code, _, stderr := run(t, dir, []string{}, "init", "--full"); code != ExitOK {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	path := filepath.Join(dir, "crier.yaml")

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, d := range config.Registry() {
		leaf := d.Key
		if i := strings.LastIndex(leaf, "."); i >= 0 {
			leaf = leaf[i+1:]
		}
		if !strings.Contains(text, leaf+":") {
			t.Errorf("--full has no line for %s", d.Key)
		}
	}

	cfg := loads(t, path)
	if err := config.Validate(&cfg); err != nil {
		t.Fatalf("--full does not validate: %v", err)
	}
	// Secrets are placeholders rather than empty strings, so it is obvious
	// which lines have to be filled in.
	if !strings.Contains(text, "<your-token>") {
		t.Error("--full should write placeholders for the secrets")
	}
	// The hint is for the starter; the reference dump does not need it.
	if _, _, stderr := run(t, t.TempDir(), []string{}, "init", "--full"); strings.Contains(stderr, "Next:") {
		t.Errorf("--full printed the starter hint: %q", stderr)
	}
}

func TestInitFormats(t *testing.T) {
	for _, tt := range []struct {
		format string
		file   string
	}{
		{"yaml", "crier.yaml"},
		{"json", "crier.json"},
		{"toml", "crier.toml"},
	} {
		for _, full := range []bool{false, true} {
			dir := t.TempDir()
			args := []string{"init", "--format", tt.format}
			if full {
				args = append(args, "--full")
			}
			code, _, stderr := run(t, dir, []string{}, args...)
			if code != ExitOK {
				t.Fatalf("%s full=%v: code = %d, stderr = %q", tt.format, full, code, stderr)
			}
			path := filepath.Join(dir, tt.file)
			cfg := loads(t, path)
			if cfg.Render.Width == 0 {
				t.Errorf("%s full=%v: nothing came back out of it", tt.format, full)
			}
		}
	}

	if code, _, stderr := run(t, t.TempDir(), []string{}, "init", "--format", "ini"); code != ExitUsage {
		t.Errorf("an unknown format = %d, want a usage error; stderr = %q", code, stderr)
	}
}

// TestInitRefusesToOverwrite is the one destructive thing init could do, so it
// is the one it refuses without being asked twice.
func TestInitRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crier.yaml")
	const mine = "render:\n  width: 42\n"
	if err := os.WriteFile(path, []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := run(t, dir, []string{}, "init")
	if code != ExitConfig {
		t.Fatalf("code = %d, want a config error", code)
	}
	if !strings.Contains(stderr, "--force") {
		t.Errorf("the refusal should say how to override it: %q", stderr)
	}
	body, _ := os.ReadFile(path)
	if string(body) != mine {
		t.Error("the existing file was overwritten anyway")
	}

	if code, _, stderr := run(t, dir, []string{}, "init", "--force"); code != ExitOK {
		t.Fatalf("--force: code = %d, stderr = %q", code, stderr)
	}
	if body, _ := os.ReadFile(path); string(body) == mine {
		t.Error("--force did not overwrite")
	}
}

func TestInitOutputElsewhere(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "nested", "config", "crier.yaml")
	if code, _, stderr := run(t, dir, []string{}, "init", "--output", out); code != ExitOK {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	loads(t, out)
	if _, err := os.Stat(filepath.Join(dir, "crier.yaml")); err == nil {
		t.Error("--output should not also write into the working directory")
	}
}
