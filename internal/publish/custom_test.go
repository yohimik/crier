package publish

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/render"
)

func customConfig(t *testing.T, command string) *config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.Publish.Custom = map[string]*config.Custom{
		"webhook": {
			Enabled: true,
			Command: command,
			Kinds:   []string{"image"},
			Format:  "png",
			Timeout: "30s",
		},
	}
	return &cfg
}

func requireShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the test commands are shell scripts")
	}
	if _, err := shell(); err != nil {
		t.Skip("no sh on PATH")
	}
}

// TestCustomEnvironmentContract is the whole interface a script sees. It is
// asserted variable by variable, because every one of them is a promise
// somebody's script depends on.
func TestCustomEnvironmentContract(t *testing.T) {
	requireShell(t)
	dump := filepath.Join(t.TempDir(), "env.txt")
	cfg := customConfig(t, "env | grep '^CRIER_' | sort > "+dump+"; echo id=abc >> \"$CRIER_OUTPUT\"; "+
		"echo link=https://example.test/abc >> \"$CRIER_OUTPUT\"")
	cfg.Publish.Custom["webhook"].NeedsURL = true
	cfg.Publish.Custom["webhook"].Env = map[string]string{"MY_TOKEN": "s3cret"}

	art := imageArtifact(t)
	res, err := onlyPublisher(t, cfg).Publish(context.Background(), Input{
		Artifact: art,
		URL:      "https://staged.example/x.jpg",
		Caption:  "hello from crier",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "abc" || res.URL != "https://example.test/abc" {
		t.Errorf("result = %+v, want the id and link the script reported", res)
	}

	body, err := os.ReadFile(dump)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			env[k] = v
		}
	}
	for key, want := range map[string]string{
		EnvPlatform:       "webhook",
		EnvArtifact:       art.Path,
		EnvArtifactFormat: string(art.Format),
		EnvArtifactKind:   string(art.Kind),
		EnvArtifactType:   art.ContentType,
		EnvURL:            "https://staged.example/x.jpg",
		EnvCaption:        "hello from crier",
	} {
		if env[key] != want {
			t.Errorf("%s = %q, want %q", key, env[key], want)
		}
	}
	if env[EnvOutput] == "" {
		t.Error("CRIER_OUTPUT was not set, so a script has nowhere to report to")
	}
	// The entry's own variables reach the script too, which is how a token
	// gets there without being written into the command.
	if env["MY_TOKEN"] != "" && env["MY_TOKEN"] != "s3cret" {
		t.Errorf("MY_TOKEN = %q", env["MY_TOKEN"])
	}
}

// TestCustomUrlOnlyWhenAsked: a script has to be able to tell "no URL was
// needed" from "staging produced nothing".
func TestCustomUrlOnlyWhenAsked(t *testing.T) {
	requireShell(t)
	dump := filepath.Join(t.TempDir(), "env.txt")
	cfg := customConfig(t, "env > "+dump)

	if _, err := onlyPublisher(t, cfg).Publish(context.Background(), Input{
		Artifact: imageArtifact(t), URL: "https://staged.example/x.jpg",
	}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(dump)
	if strings.Contains(string(body), EnvURL+"=") {
		t.Error("CRIER_URL was set for a platform that did not ask to be staged")
	}
}

func TestCustomNonZeroExitIsAFailure(t *testing.T) {
	requireShell(t)
	cfg := customConfig(t, "echo 'the api said no' >&2; exit 3")
	_, err := onlyPublisher(t, cfg).Publish(context.Background(), Input{Artifact: imageArtifact(t)})
	if err == nil {
		t.Fatal("a non-zero exit should be a failure")
	}
	// The tail is the only thing that says why, so it has to be in the error.
	if !strings.Contains(err.Error(), "the api said no") {
		t.Errorf("err = %v, want the script's own output", err)
	}
}

func TestCustomTimeoutKillsTheCommand(t *testing.T) {
	requireShell(t)
	cfg := customConfig(t, "sleep 30")
	cfg.Publish.Custom["webhook"].Timeout = "150ms"

	_, err := onlyPublisher(t, cfg).Publish(context.Background(), Input{Artifact: imageArtifact(t)})
	if err == nil || !strings.Contains(err.Error(), "did not finish within") {
		t.Fatalf("err = %v, want a timeout", err)
	}
}

// TestCustomOutputParsing covers the reporting half of the contract, including
// what happens when a script says nothing at all.
func TestCustomOutputParsing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(path, []byte(
		"# a comment\n\nid = 42 \nlink=https://example.test/42\nurl=https://example.test/other\n"+
			"account=@crier\nnot a pair\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := readCustomOutput(path)
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "42" {
		t.Errorf("id = %q", res.ID)
	}
	// url is an accepted spelling of link, and the last one wins.
	if res.URL != "https://example.test/other" {
		t.Errorf("url = %q", res.URL)
	}
	// Anything crier has no field for is kept rather than dropped.
	if res.Extra["account"] != "@crier" {
		t.Errorf("extra = %v", res.Extra)
	}

	// A script that reports nothing is a valid publisher: exit 0 is the claim.
	empty, err := readCustomOutput(filepath.Join(dir, "missing.txt"))
	if err != nil || empty.ID != "" {
		t.Errorf("a missing output file = %+v, %v", empty, err)
	}
}

func TestCustomNeedsFromConfig(t *testing.T) {
	cfg := customConfig(t, "true")
	c := cfg.Publish.Custom["webhook"]
	c.Kinds = []string{"image", "video"}
	c.Format = "jpeg"
	c.NeedsURL = true

	needs := onlyPublisher(t, cfg).Needs()
	if !needs.URL {
		t.Error("needs-url did not reach Needs")
	}
	if needs.Formats[0] != config.JPEG {
		t.Errorf("formats = %v, want jpeg first", needs.Formats)
	}
	if !needs.Accepts(render.KindVideo) || !needs.Accepts(render.KindImage) {
		t.Errorf("kinds = %v", needs.Kinds)
	}

	// An entry that names no kinds still publishes images rather than nothing.
	c.Kinds = nil
	if got := onlyPublisher(t, cfg).Needs().Kinds; len(got) != 1 || got[0] != render.KindImage {
		t.Errorf("kinds = %v", got)
	}
}

// TestCustomPingNeverPublishes is the property that makes ping safe for a
// platform crier knows nothing about.
func TestCustomPingNeverPublishes(t *testing.T) {
	requireShell(t)
	marker := filepath.Join(t.TempDir(), "published")
	cfg := customConfig(t, "touch "+marker)

	id, err := onlyPublisher(t, cfg).Ping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("ping ran the publish command")
	}
	if !strings.Contains(id.Note, "ping-command") {
		t.Errorf("the note should say why nothing was checked: %+v", id)
	}

	// With a ping-command it runs that one, and reports what it says.
	cfg.Publish.Custom["webhook"].PingCommand = `echo id=acct-1 >> "$CRIER_OUTPUT"; echo name=@crier >> "$CRIER_OUTPUT"`
	id, err = onlyPublisher(t, cfg).Ping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id.ID != "acct-1" || id.Name != "@crier" {
		t.Errorf("identity = %+v", id)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("ping ran the publish command anyway")
	}

	cfg.Publish.Custom["webhook"].PingCommand = "exit 1"
	if _, err := onlyPublisher(t, cfg).Ping(context.Background()); err == nil {
		t.Error("a failing ping-command should fail the ping")
	}
}

func TestCustomWithoutACommandIsAConfigError(t *testing.T) {
	cfg := customConfig(t, "")
	if _, err := Build(cfg, testDeps(t)); err == nil {
		t.Fatal("an enabled custom platform needs a command")
	}
}

// TestCustomIsAPeer checks the plumbing that makes it one rather than a
// special case: it is enabled, built and named like any other platform.
func TestCustomIsAPeer(t *testing.T) {
	requireShell(t)
	cfg := customConfig(t, "true")
	cfg.Publish.Telegram.Enabled = true
	cfg.Publish.Telegram.Token = "t"
	cfg.Publish.Telegram.ChatID = "c"

	enabled := Enabled(cfg)
	if len(enabled) != 2 || enabled[0] != "telegram" || enabled[1] != "webhook" {
		t.Fatalf("enabled = %v, want the built-in then the custom", enabled)
	}
	built, err := Build(cfg, testDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(built) != 2 || built[1].Name() != "webhook" {
		t.Errorf("built = %v", built)
	}
}
