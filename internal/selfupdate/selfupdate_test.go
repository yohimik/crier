package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func testLogger(t *testing.T) zerolog.Logger {
	return zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.DebugLevel)
}

// --- naming ------------------------------------------------------------------

// TestAssetNameIsTheContract guards the string four other things depend on:
// the Dockerfile's cross-compile loop, install.sh, install.ps1 and
// `dispat install --asset 'crier-{os}-{arch}'`. Changing it breaks all four.
func TestAssetNameIsTheContract(t *testing.T) {
	for _, tt := range []struct{ goos, goarch, want string }{
		{"linux", "amd64", "crier-linux-amd64"},
		{"darwin", "arm64", "crier-darwin-arm64"},
		{"windows", "amd64", "crier-windows-amd64.exe"},
		{"windows", "arm64", "crier-windows-arm64.exe"},
	} {
		if got := AssetName(tt.goos, tt.goarch); got != tt.want {
			t.Errorf("AssetName(%s, %s) = %q, want %q", tt.goos, tt.goarch, got, tt.want)
		}
	}
	if CurrentAssetName() != AssetName(runtime.GOOS, runtime.GOARCH) {
		t.Error("CurrentAssetName should be this platform's")
	}
}

func TestBackupPathKeepsTheExtension(t *testing.T) {
	if got := BackupPath("/usr/local/bin/crier"); got != "/usr/local/bin/crier.backup" {
		t.Errorf("= %q", got)
	}
	// Windows will not execute a file with no extension, and a backup that
	// cannot be run can be neither verified nor used by hand.
	if got := BackupPath(`C:\bin\crier.exe`); got != `C:\bin\crier.backup.exe` {
		t.Errorf("= %q", got)
	}
}

func TestDescribeTellsTheThreeBuildsApart(t *testing.T) {
	if b := Describe("1.2.3"); b.Origin != OriginRelease || b.Version != "1.2.3" {
		t.Errorf("stamped = %+v", b)
	}
	// The leading v the release tags carry is not part of the version.
	if b := Describe("v1.2.3"); b.Version != "1.2.3" {
		t.Errorf("v-prefixed = %+v", b)
	}
	// Under `go test` there is build info but no module version, so this is
	// the local-build case.
	if b := Describe("dev"); b.Origin == OriginRelease {
		t.Errorf("dev = %+v, want a non-release origin", b)
	}
}

// --- pruning -------------------------------------------------------------------

func TestPruneBackupWaitsOutTheTTL(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "crier")
	backup := BackupPath(exe)
	write(t, backup, "old")

	now := time.Now()
	if PruneBackup(exe, now) {
		t.Error("a fresh backup should be kept")
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatal("it was deleted anyway")
	}

	if !PruneBackup(exe, now.Add(BackupTTL+time.Hour)) {
		t.Error("an expired backup should go")
	}
	if _, err := os.Stat(backup); err == nil {
		t.Error("it is still there")
	}

	// Nothing to prune is the common case and must be silent and cheap.
	if PruneBackup(exe, now) {
		t.Error("pruning nothing reported a deletion")
	}
	if PruneBackup("", now) {
		t.Error("an empty path should do nothing")
	}
}

// --- the swap ------------------------------------------------------------------

func TestReplaceKeepsTheOutgoingBinary(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "crier")
	write(t, exe, "old")
	incoming := filepath.Join(dir, "incoming")
	write(t, incoming, "new")

	backup, err := Replace(exe, incoming)
	if err != nil {
		t.Fatal(err)
	}
	if backup != BackupPath(exe) {
		t.Errorf("backup = %q", backup)
	}
	if read(t, exe) != "new" || read(t, backup) != "old" {
		t.Errorf("exe = %q, backup = %q", read(t, exe), read(t, backup))
	}
	// The backup's clock starts at the swap rather than carrying the mtime of
	// whatever it used to be, or PruneBackup would misjudge its age.
	info, err := os.Stat(backup)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(info.ModTime()) > time.Minute {
		t.Errorf("the backup's mtime was not reset: %v", info.ModTime())
	}
}

func TestReplaceIntoAnEmptyPathIsAFirstInstall(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "crier")
	incoming := filepath.Join(dir, "incoming")
	write(t, incoming, "new")

	backup, err := Replace(exe, incoming)
	if err != nil {
		t.Fatal(err)
	}
	if backup != "" {
		t.Errorf("backup = %q, want none: there was nothing to keep", backup)
	}
	if read(t, exe) != "new" {
		t.Error("the binary was not installed")
	}
}

// TestRestoreRotates is what makes a rollback reversible: no version is lost,
// so a second rollback returns.
func TestRestoreRotates(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "crier")
	write(t, exe, "new")
	write(t, BackupPath(exe), "old")

	if err := Restore(exe); err != nil {
		t.Fatal(err)
	}
	if read(t, exe) != "old" || read(t, BackupPath(exe)) != "new" {
		t.Fatalf("exe = %q, backup = %q", read(t, exe), read(t, BackupPath(exe)))
	}
	if err := Restore(exe); err != nil {
		t.Fatal(err)
	}
	if read(t, exe) != "new" {
		t.Error("a second rollback should return")
	}
}

func TestRestoreWithNoBackup(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "crier")
	write(t, exe, "new")
	if err := Restore(exe); err == nil || !strings.Contains(err.Error(), "no backup") {
		t.Errorf("err = %v", err)
	}
}

// --- version parsing -----------------------------------------------------------

func TestParseVersionOutput(t *testing.T) {
	line := "crier 1.2.3 (commit abc, built 2026-01-01T00:00:00Z, go1.26.0, linux/amd64)"
	if got := ParseVersionOutput(line); got != "1.2.3" {
		t.Errorf("= %q", got)
	}
	if got := ParseVersionOutput("some noise\n" + line + "\nmore"); got != "1.2.3" {
		t.Errorf("with noise = %q", got)
	}
	// Anything that does not read that way is "cannot tell", never a match.
	if got := ParseVersionOutput("not a version line"); got != "" {
		t.Errorf("= %q, want empty", got)
	}
}

// --- the token boundary --------------------------------------------------------

// TestTokenGoesOnlyToGitHub is a privacy rule with teeth: --api-url is how
// somebody points crier at a mirror, and a mirror must not receive the token
// they keep for github.com.
func TestTokenGoesOnlyToGitHub(t *testing.T) {
	env := []string{"GITHUB_TOKEN=secret", "OTHER=x"}
	for _, api := range []string{"", "https://api.github.com", "https://github.com"} {
		if got := TokenFrom(env, "GITHUB_TOKEN", api); got != "secret" {
			t.Errorf("TokenFrom(%q) = %q, want the token", api, got)
		}
	}
	for _, api := range []string{"https://ghe.example.com/api/v3", "http://127.0.0.1:8080", "://bad"} {
		if got := TokenFrom(env, "GITHUB_TOKEN", api); got != "" {
			t.Errorf("TokenFrom(%q) = %q, want nothing sent elsewhere", api, got)
		}
	}
	if got := TokenFrom(env, "", ""); got != "" {
		t.Errorf("no variable named = %q", got)
	}
	if got := TokenFrom(env, "MISSING", ""); got != "" {
		t.Errorf("an unset variable = %q", got)
	}
}

// --- the release listing -------------------------------------------------------

// fakeGitHub serves a releases listing and the assets attached to it.
type fakeGitHub struct {
	*httptest.Server
	releases []map[string]any
	assets   map[string][]byte
	// corrupt makes the asset server hand back something other than what the
	// digest promises.
	corrupt bool
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
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(rel)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		case strings.HasPrefix(r.URL.Path, "/assets/"):
			body, ok := f.assets[strings.TrimPrefix(r.URL.Path, "/assets/")]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if f.corrupt {
				body = append([]byte("tampered"), body[8:]...)
			}
			_, _ = w.Write(body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

// release adds one release carrying an asset for this platform.
func (f *fakeGitHub) release(tag string, prerelease bool, body []byte) {
	name := CurrentAssetName()
	f.assets[tag+"-"+name] = body
	sum := sha256.Sum256(body)
	f.releases = append(f.releases, map[string]any{
		"tag_name":   tag,
		"prerelease": prerelease,
		"body":       "notes for " + tag,
		"html_url":   "https://github.com/yohimik/crier/releases/tag/" + tag,
		"assets": []map[string]any{{
			"name":                 name,
			"browser_download_url": f.URL + "/assets/" + tag + "-" + name,
			"size":                 len(body),
			"digest":               "sha256:" + hex.EncodeToString(sum[:]),
		}},
	})
}

func (f *fakeGitHub) source(t *testing.T, prerelease bool) *Source {
	return &Source{APIURL: f.URL, Prerelease: prerelease, Log: testLogger(t)}
}

// TestLatestPicksTheHighestNotTheNewest is the rule that keeps a patch cut on
// an old line from looking like an upgrade.
func TestLatestPicksTheHighestNotTheNewest(t *testing.T) {
	f := newFakeGitHub(t)
	f.release("v1.4.0", false, []byte("newest-line"))
	f.release("v1.2.5", false, []byte("old-line-patch")) // published later, lower
	f.release("not-a-version", false, []byte("noise"))

	rel, err := f.source(t, false).Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version.String() != "1.4.0" {
		t.Errorf("selected %s, want 1.4.0", rel.Version)
	}
}

func TestLatestSkipsPrereleasesUnlessAsked(t *testing.T) {
	f := newFakeGitHub(t)
	f.release("v1.0.0-rc.1", true, []byte("rc"))

	if _, err := f.source(t, false).Latest(context.Background()); err == nil {
		t.Fatal("a prerelease-only repository should have no stable release")
	}
	rel, err := f.source(t, true).Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version.String() != "1.0.0-rc.1" {
		t.Errorf("= %s", rel.Version)
	}

	// A release beats its own candidate even when both are in the running.
	f.release("v1.0.0", false, []byte("final"))
	rel, err = f.source(t, true).Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version.String() != "1.0.0" {
		t.Errorf("= %s, want the release rather than its candidate", rel.Version)
	}
}

func TestAtLooksUpOneVersion(t *testing.T) {
	f := newFakeGitHub(t)
	f.release("v1.1.0", false, []byte("one-one"))
	src := f.source(t, false)

	rel, err := src.At(context.Background(), "1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if rel.Tag != "v1.1.0" {
		t.Errorf("tag = %q", rel.Tag)
	}
	// The v is optional on the way in.
	if _, err := src.At(context.Background(), "v1.1.0"); err != nil {
		t.Errorf("a v-prefixed version should work: %v", err)
	}
	// A version nobody published is not "you are up to date".
	if _, err := src.At(context.Background(), "9.9.9"); err == nil {
		t.Error("an unpublished version should fail")
	}
	if _, err := src.At(context.Background(), "not-a-version"); err == nil {
		t.Error("a non-version should fail")
	}
}

func TestAssetLookupNamesWhatIsThere(t *testing.T) {
	rel := Release{Assets: []Asset{{Name: "crier-plan9-386"}}}
	if _, ok := rel.Asset(runtime.GOOS, runtime.GOARCH); ok {
		t.Error("this platform is not in that release")
	}
	if names := rel.AssetNames(); len(names) != 1 || names[0] != "crier-plan9-386" {
		t.Errorf("names = %v", names)
	}
}

func TestNextLink(t *testing.T) {
	header := `<https://api.github.com/x?page=2>; rel="next", <https://api.github.com/x?page=9>; rel="last"`
	if got := nextLink(header); got != "https://api.github.com/x?page=2" {
		t.Errorf("= %q", got)
	}
	// A header with no next, and a malformed one, are both "one page".
	if got := nextLink(`<https://x>; rel="last"`); got != "" {
		t.Errorf("= %q", got)
	}
	if got := nextLink("garbage"); got != "" {
		t.Errorf("= %q", got)
	}
}

// --- the install ---------------------------------------------------------------

// script is a tiny executable that prints a crier version line, which is what
// the smoke test runs the download against.
func script(version string) []byte {
	return []byte("#!/bin/sh\necho \"crier " + version +
		" (commit abc, built now, go1.26.0, test/test)\"\n")
}

func TestInstallVerifiesAndSwaps(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	f := newFakeGitHub(t)
	f.release("v2.0.0", false, script("2.0.0"))

	dir := t.TempDir()
	exe := filepath.Join(dir, "crier")
	write(t, exe, "#!/bin/sh\necho old\n")
	if err := os.Chmod(exe, 0o755); err != nil {
		t.Fatal(err)
	}

	rel, err := f.source(t, false).Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	asset, ok := rel.Asset(runtime.GOOS, runtime.GOARCH)
	if !ok {
		t.Fatal("no asset for this platform")
	}

	inst := &Installer{Exe: exe, Want: "2.0.0", Log: testLogger(t)}
	backup, err := inst.Install(context.Background(), asset)
	if err != nil {
		t.Fatal(err)
	}
	if backup != BackupPath(exe) {
		t.Errorf("backup = %q", backup)
	}
	if !strings.Contains(read(t, exe), "crier 2.0.0") {
		t.Errorf("the new binary is not in place: %q", read(t, exe))
	}
	if read(t, backup) != "#!/bin/sh\necho old\n" {
		t.Errorf("the old binary was not kept: %q", read(t, backup))
	}

	// And the rollback puts it back, which is the point of keeping it.
	from, to, err := Rollback(context.Background(), exe)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if from != "2.0.0" {
		t.Errorf("rolled back from %q", from)
	}
	_ = to // the old script prints no crier line, so it reports no version
	if strings.Contains(read(t, exe), "crier 2.0.0") {
		t.Error("the rollback did not take")
	}
}

// TestInstallRefusesATamperedDownload is the security-relevant one: a body
// that does not hash to what the release published must never reach the path
// the running binary occupies.
func TestInstallRefusesATamperedDownload(t *testing.T) {
	f := newFakeGitHub(t)
	f.release("v2.0.0", false, script("2.0.0"))
	f.corrupt = true

	dir := t.TempDir()
	exe := filepath.Join(dir, "crier")
	write(t, exe, "original")

	rel, err := f.source(t, false).Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	asset, _ := rel.Asset(runtime.GOOS, runtime.GOARCH)

	inst := &Installer{Exe: exe, Log: testLogger(t)}
	_, err = inst.Install(context.Background(), asset)
	if err == nil || !strings.Contains(err.Error(), "hashes to") {
		t.Fatalf("err = %v, want a digest mismatch", err)
	}
	if read(t, exe) != "original" {
		t.Error("the running binary was replaced with something unverified")
	}
	// The failed download left nothing behind.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("the staged download was not cleaned up: %v", names(entries))
	}
}

// TestInstallRefusesAShortDownload covers the other half of the verification,
// for a release whose digest is missing.
func TestInstallRefusesAShortDownload(t *testing.T) {
	body := script("2.0.0")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body[:4])
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	exe := filepath.Join(dir, "crier")
	write(t, exe, "original")

	inst := &Installer{Exe: exe, Log: testLogger(t)}
	_, err := inst.Install(context.Background(),
		Asset{Name: "crier", URL: srv.URL, Size: int64(len(body))})
	if err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("err = %v, want the size mismatch", err)
	}
	if read(t, exe) != "original" {
		t.Error("the running binary was replaced by a truncated download")
	}
}

// TestInstallRefusesABinaryThatDoesNotRun is the last gate before the swap: a
// file can download perfectly and still be the wrong thing.
func TestInstallRefusesABinaryThatDoesNotRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	f := newFakeGitHub(t)
	f.release("v2.0.0", false, script("1.0.0")) // claims a different version

	dir := t.TempDir()
	exe := filepath.Join(dir, "crier")
	write(t, exe, "original")

	rel, _ := f.source(t, false).Latest(context.Background())
	asset, _ := rel.Asset(runtime.GOOS, runtime.GOARCH)

	inst := &Installer{Exe: exe, Want: "2.0.0", Log: testLogger(t)}
	if _, err := inst.Install(context.Background(), asset); err == nil {
		t.Fatal("a binary reporting the wrong version should be refused")
	}
	if read(t, exe) != "original" {
		t.Error("the running binary was replaced by the wrong version")
	}
}

func TestInstallReportsAnUnwritableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes everywhere")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "sub", "crier")
	if err := os.MkdirAll(filepath.Dir(exe), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(exe), 0o700) })

	inst := &Installer{Exe: exe, Log: testLogger(t)}
	_, err := inst.Install(context.Background(), Asset{Name: "crier", URL: "http://127.0.0.1:1"})
	if err == nil || !strings.Contains(err.Error(), "rights to replace") {
		t.Fatalf("err = %v, want the advice about permissions", err)
	}
}

func TestBackupVersionWithNoBackup(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "crier")
	if _, err := BackupVersion(context.Background(), exe); err == nil {
		t.Error("there is no backup")
	}
	if _, _, err := Rollback(context.Background(), exe); err == nil {
		t.Error("there is nothing to roll back to")
	}
}

// --- helpers -------------------------------------------------------------------

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil { //nolint:gosec // a fake binary has to be runnable
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func names(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}
