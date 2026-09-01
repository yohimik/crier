//go:build e2e

package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The self-update tests drive the real command against a fake GitHub: a
// releases listing, an asset server, and a real crier binary stamped with a
// version to update to. Nothing here is mocked inside the program — the
// binary being replaced is a binary on disk, and the one replacing it is one
// the Go toolchain built.

// assetName mirrors internal/selfupdate.AssetName, spelled out rather than
// imported: an end-to-end test asserts the contract as the outside world sees
// it, and the outside world reads the file names off a release page.
func assetName() string {
	name := "crier-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// buildStamped builds crier with a version baked in, which is what makes an
// update verifiable: the downloaded binary has to report the version the
// release promised before anything is moved.
func buildStamped(t *testing.T, version string) []byte {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "crier-"+version)
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build",
		"-ldflags", "-X github.com/yohimik/crier/internal/version.Version="+version,
		"-o", out, "./cmd/crier")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building a stamped crier: %v\n%s", err, combined)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// fakeGitHub serves a releases listing and the assets attached to it.
type fakeGitHub struct {
	*httptest.Server
	releases []map[string]any
	assets   map[string][]byte
	corrupt  bool
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{assets: map[string][]byte{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(f.releases)
		case strings.Contains(r.URL.Path, "/releases/tags/"):
			tag := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			for _, rel := range f.releases {
				if rel["tag_name"] == tag {
					_ = json.NewEncoder(w).Encode(rel)
					return
				}
			}
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		case strings.HasPrefix(r.URL.Path, "/assets/"):
			body, ok := f.assets[strings.TrimPrefix(r.URL.Path, "/assets/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			if f.corrupt {
				body = append([]byte("tampered!"), body[9:]...)
			}
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeGitHub) release(tag string, prerelease bool, body []byte) {
	key := tag + "-" + assetName()
	f.assets[key] = body
	sum := sha256.Sum256(body)
	f.releases = append(f.releases, map[string]any{
		"tag_name":   tag,
		"prerelease": prerelease,
		"body":       "what changed in " + tag,
		"html_url":   "https://github.com/yohimik/crier/releases/tag/" + tag,
		"assets": []map[string]any{{
			"name":                 assetName(),
			"browser_download_url": f.URL + "/assets/" + key,
			"size":                 len(body),
			"digest":               "sha256:" + hex.EncodeToString(sum[:]),
		}},
	})
}

// installed puts a copy of the binary under test in its own directory, so an
// update replaces that copy rather than the one every other test is using.
func installed(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(crierBin)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "crier")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	if err := os.WriteFile(path, body, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// runAt drives a specific binary rather than the shared one.
func runAt(t *testing.T, bin string, args ...string) result {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "GOCOVERDIR="+coverDir)
	return runCmd(t, cmd)
}

func versionOf(t *testing.T, bin string) string {
	t.Helper()
	res := runAt(t, bin, "--version")
	if res.Code != exitOK {
		t.Fatalf("%s --version: code=%d stderr=%s", bin, res.Code, res.Stderr)
	}
	rest, ok := strings.CutPrefix(strings.TrimSpace(res.Stdout), "crier ")
	if !ok {
		t.Fatalf("unexpected version line: %q", res.Stdout)
	}
	v, _, _ := strings.Cut(rest, " (")
	return v
}

// TestSelfUpdateReplacesTheBinaryAndRollsBack is the whole loop: update, check
// the new binary is in place and reports the released version, then roll back
// and check the old one returns.
func TestSelfUpdateReplacesTheBinaryAndRollsBack(t *testing.T) {
	f := newFakeGitHub(t)
	f.release("v9.9.9", false, buildStamped(t, "9.9.9"))

	bin := installed(t)
	before := versionOf(t, bin)

	res := runAt(t, bin, "self-update", "--api-url", f.URL)
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "9.9.9") {
		t.Errorf("stdout = %q", res.Stdout)
	}
	if got := versionOf(t, bin); got != "9.9.9" {
		t.Fatalf("after the update the binary reports %q", got)
	}

	backup := strings.TrimSuffix(bin, filepath.Ext(bin)) + ".backup" + filepath.Ext(bin)
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("the replaced binary was not kept: %v", err)
	}

	res = runAt(t, bin, "self-update", "--rollback")
	if res.Code != exitOK {
		t.Fatalf("rollback: code=%d stderr=%s", res.Code, res.Stderr)
	}
	if got := versionOf(t, bin); got != before {
		t.Errorf("after the rollback the binary reports %q, want %q", got, before)
	}
	// The rotation keeps what it replaced, so a second rollback returns.
	if res := runAt(t, bin, "self-update", "--rollback"); res.Code != exitOK {
		t.Fatalf("second rollback: code=%d stderr=%s", res.Code, res.Stderr)
	}
	if got := versionOf(t, bin); got != "9.9.9" {
		t.Errorf("a second rollback should return: %q", got)
	}
}

// TestSelfUpdateCheckChangesNothing covers the read-only mode, and the exit
// code a script branches on.
func TestSelfUpdateCheckChangesNothing(t *testing.T) {
	f := newFakeGitHub(t)
	f.release("v9.9.9", false, []byte("not even a binary"))

	bin := installed(t)
	before := versionOf(t, bin)

	res := runAt(t, bin, "self-update", "--check", "--api-url", f.URL)
	if res.Code != exitConfig {
		t.Fatalf("--check with a newer release = %d, want 1: %s", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "9.9.9") {
		t.Errorf("stdout = %q", res.Stdout)
	}
	if got := versionOf(t, bin); got != before {
		t.Errorf("--check replaced the binary: %q", got)
	}

	// And the JSON shape, which is what a script actually reads.
	res = runAt(t, bin, "self-update", "--check", "--json", "--api-url", f.URL)
	var rep struct {
		Current string `json:"current"`
		Latest  string `json:"latest"`
		Updated bool   `json:"updated"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &rep); err != nil {
		t.Fatalf("%v\n%s", err, res.Stdout)
	}
	if rep.Latest != "9.9.9" || rep.Updated {
		t.Errorf("report = %+v", rep)
	}
}

// TestSelfUpdateRefusesATamperedAsset is the one that matters most: a download
// that does not hash to what the release published must never land.
func TestSelfUpdateRefusesATamperedAsset(t *testing.T) {
	f := newFakeGitHub(t)
	f.release("v9.9.9", false, buildStamped(t, "9.9.9"))
	f.corrupt = true

	bin := installed(t)
	before := versionOf(t, bin)

	res := runAt(t, bin, "self-update", "--api-url", f.URL)
	if res.Code == exitOK {
		t.Fatal("a tampered asset was installed")
	}
	if !strings.Contains(res.Stderr, "hashes to") {
		t.Errorf("stderr should name the digest mismatch: %s", res.Stderr)
	}
	if got := versionOf(t, bin); got != before {
		t.Errorf("the binary changed anyway: %q", got)
	}
	// Nothing was left behind in the install directory.
	entries, err := os.ReadDir(filepath.Dir(bin))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("the staged download was not cleaned up: %v", names)
	}
}

// TestSelfUpdateSkipsPrereleasesUnlessAsked: a release candidate must not
// arrive on a machine that did not ask for one.
func TestSelfUpdateSkipsPrereleasesUnlessAsked(t *testing.T) {
	f := newFakeGitHub(t)
	f.release("v9.9.9-rc.1", true, buildStamped(t, "9.9.9-rc.1"))

	bin := installed(t)
	before := versionOf(t, bin)

	res := runAt(t, bin, "self-update", "--api-url", f.URL)
	if res.Code == exitOK {
		t.Fatal("a release candidate was installed without --prerelease")
	}
	if !strings.Contains(res.Stderr, "--prerelease") {
		t.Errorf("the refusal should say how to opt in: %s", res.Stderr)
	}
	if got := versionOf(t, bin); got != before {
		t.Errorf("the binary changed: %q", got)
	}

	res = runAt(t, bin, "self-update", "--prerelease", "--api-url", f.URL)
	if res.Code != exitOK {
		t.Fatalf("with --prerelease: code=%d stderr=%s", res.Code, res.Stderr)
	}
	if got := versionOf(t, bin); got != "9.9.9-rc.1" {
		t.Errorf("= %q", got)
	}
}

// TestSelfUpdateAtAVersion pins an install rather than taking the newest.
func TestSelfUpdateAtAVersion(t *testing.T) {
	f := newFakeGitHub(t)
	f.release("v9.9.9", false, buildStamped(t, "9.9.9"))
	f.release("v9.8.0", false, buildStamped(t, "9.8.0"))

	bin := installed(t)
	res := runAt(t, bin, "self-update", "--release", "9.8.0", "--api-url", f.URL)
	if res.Code != exitOK {
		t.Fatalf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	if got := versionOf(t, bin); got != "9.8.0" {
		t.Errorf("= %q, want the pinned version rather than the newest", got)
	}

	res = runAt(t, bin, "self-update", "--release", "1.2.3", "--api-url", f.URL)
	if res.Code == exitOK {
		t.Error("a version nobody published should fail")
	}
}

func TestSelfUpdateWithNoReleaseAtAll(t *testing.T) {
	f := newFakeGitHub(t)
	bin := installed(t)
	res := runAt(t, bin, "self-update", "--api-url", f.URL)
	if res.Code == exitOK || !strings.Contains(res.Stderr, "no matching release") {
		t.Errorf("code=%d stderr=%s", res.Code, res.Stderr)
	}
}

func TestSelfUpdateRollbackWithNothingToRollBackTo(t *testing.T) {
	bin := installed(t)
	res := runAt(t, bin, "self-update", "--rollback")
	if res.Code == exitOK || !strings.Contains(res.Stderr, "no backup") {
		t.Errorf("code=%d stderr=%s", res.Code, res.Stderr)
	}
}

func TestSelfUpdateUsageErrors(t *testing.T) {
	bin := installed(t)
	res := runAt(t, bin, "self-update", "--check", "--rollback")
	if res.Code != exitUsage {
		t.Errorf("code=%d stderr=%s", res.Code, res.Stderr)
	}
	if res := runAt(t, bin, "self-update", "--nope"); res.Code != exitUsage {
		t.Errorf("an unknown flag = %d", res.Code)
	}
}
