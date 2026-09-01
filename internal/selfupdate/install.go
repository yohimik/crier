package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

const (
	// maxAsset bounds the download. A crier binary is about 30 MiB; this is
	// the point past which the answer is not a crier release.
	maxAsset = 256 << 20
	// downloadTimeout is the default client's timeout. It covers the whole
	// transfer, so it is generous: a slow link is not a failure.
	downloadTimeout = 10 * time.Minute
	// smokeTimeout bounds the "does the new binary run" check.
	smokeTimeout = 30 * time.Second
)

// Installer puts a downloaded release asset in a binary's place.
type Installer struct {
	// Exe is the binary to replace. Empty means the running one.
	Exe    string
	Client *http.Client // default: a 10m-timeout client
	// Want is the version the downloaded binary has to report before anything
	// is moved. Empty accepts whatever runs.
	Want string
	// Command is the command word this installer's failures name.
	Command string
	Log     zerolog.Logger
}

// what names this installer in the errors it returns.
func (i *Installer) what() string { return commandOr(i.Command) }

func (i *Installer) exe() (string, error) {
	if i.Exe != "" {
		return i.Exe, nil
	}
	return Executable()
}

func (i *Installer) client() *http.Client {
	if i.Client != nil {
		return i.Client
	}
	return &http.Client{Timeout: downloadTimeout}
}

// Install downloads the asset, satisfies itself that what arrived is the binary
// it asked for, and puts it in place. It answers where the outgoing binary was
// kept, which is empty when the path held nothing to keep.
//
// Nothing is moved until every check has passed, so a failed update leaves the
// working binary exactly where it was.
func (i *Installer) Install(ctx context.Context, a Asset) (backup string, err error) {
	exe, err := i.exe()
	if err != nil {
		return "", fmt.Errorf("%s: locating the running binary: %w", i.what(), err)
	}
	dir := filepath.Dir(exe)

	tmpName, err := i.fetch(ctx, a, dir, exe)
	if err != nil {
		if errors.Is(err, ErrNotWritable) {
			return "", fmt.Errorf("%w; re-run with the rights to replace %s", err, exe)
		}
		return "", err
	}
	defer func() {
		// On every failure path below the download is removed; on success it
		// has been renamed away and this finds nothing.
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()

	// The replacement inherits the mode of the binary it replaces, so an
	// install that was deliberately group-only stays that way rather than
	// being widened to whatever the umask allows. Owner-execute is the one bit
	// forced on, because the next step runs the file. A path nothing occupies
	// yet has no mode to inherit and takes 0755.
	mode := os.FileMode(0o755)
	if info, statErr := os.Stat(exe); statErr == nil {
		mode = info.Mode().Perm() | 0o100
	}
	if err = os.Chmod(tmpName, mode); err != nil {
		return "", fmt.Errorf("%s: %s: %w", i.what(), tmpName, err)
	}
	if err = smokeTest(ctx, tmpName, i.Want); err != nil {
		return "", err
	}
	return Replace(exe, tmpName)
}

// fetch downloads the asset into dir and answers the file it wrote.
//
// The file is staged in dir rather than in the system temp folder for two
// reasons: a rename out of it must not cross a filesystem, and a directory that
// cannot be written to should cost a refusal rather than thirty megabytes. On
// every failure the staged file is removed.
//
// target is the file the download is destined for. Only its extension is read,
// and it matters on Windows: the staged file keeps it, because Windows will not
// execute a file that carries none, and the version check runs what was staged.
func (i *Installer) fetch(ctx context.Context, a Asset, dir, target string) (path string, err error) {
	tmp, err := os.CreateTemp(dir, tempPattern(target, "download"))
	if err != nil {
		return "", fmt.Errorf("%s: %s: %w (%v)", i.what(), dir, ErrNotWritable, err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()

	if err = i.download(ctx, a, tmp); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err = tmp.Close(); err != nil {
		return "", fmt.Errorf("%s: %s: %w", i.what(), tmpName, err)
	}
	i.Log.Debug().Str("asset", a.Name).Str("staged", tmpName).
		Msg(i.what() + ": asset downloaded and verified")
	return tmpName, nil
}

// download streams the asset into f, checking as it goes that what arrives is
// the size the release advertised and hashes to the digest it published.
//
// The request carries no Authorization header on purpose. The download URL
// redirects to object storage, and Go forwards headers across redirects, so an
// authenticated request would arrive at a host that rejects it. A release asset
// needs no credentials anyway.
func (i *Installer) download(ctx context.Context, a Asset, f *os.File) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return fmt.Errorf("%s: %w", i.what(), err)
	}
	resp, err := i.client().Do(req)
	if err != nil {
		return fmt.Errorf("%s: downloading %s: %w", i.what(), a.Name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: downloading %s: unexpected status %s", i.what(), a.Name, resp.Status)
	}

	sum := sha256.New()
	n, err := io.Copy(f, io.LimitReader(io.TeeReader(resp.Body, sum), maxAsset+1))
	if err != nil {
		return fmt.Errorf("%s: downloading %s: %w", i.what(), a.Name, err)
	}
	if n > maxAsset {
		return fmt.Errorf("%s: %s is larger than %d bytes", i.what(), a.Name, int64(maxAsset))
	}
	if a.Size > 0 && n != a.Size {
		return fmt.Errorf("%s: %s is %d bytes, the release says %d: the download is incomplete",
			i.what(), a.Name, n, a.Size)
	}
	want, ok := strings.CutPrefix(a.Digest, "sha256:")
	if !ok {
		// Older GitHub Enterprise versions publish no digest. The size check
		// above is then the whole of what stands between a truncated transfer
		// and an install, which is worth saying out loud.
		i.Log.Warn().Str("asset", a.Name).
			Msg(i.what() + ": the release publishes no checksum for this asset; only its size is verified")
		return nil
	}
	if got := hex.EncodeToString(sum.Sum(nil)); !strings.EqualFold(got, want) {
		return fmt.Errorf("%s: %s hashes to %s, the release says %s: refusing to install it",
			i.what(), a.Name, got, want)
	}
	i.Log.Debug().Str("asset", a.Name).Msg(i.what() + ": checksum matches the release digest")
	return nil
}

// smokeTest runs the binary before trusting it with the running one's place.
//
// A file that downloaded intact can still be the wrong thing entirely, and
// finding that out after the swap means finding it out with no crier left.
func smokeTest(ctx context.Context, path, want string) error {
	ctx, cancel := context.WithTimeout(ctx, smokeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("self-update: the downloaded binary does not run: %w", err)
	}
	if want != "" && !strings.Contains(string(out), want) {
		return fmt.Errorf("self-update: the downloaded binary reports %q rather than %s",
			strings.TrimSpace(string(out)), want)
	}
	return nil
}

// TokenFrom reads the API token out of the environment, and only for a host
// that should be sent one.
//
// A token meant for github.com must not travel to whatever host --api-url
// names: an operator pointing crier at a mirror to test something would
// otherwise hand that mirror their GitHub credentials.
func TokenFrom(environ []string, envName, apiURL string) string {
	if envName == "" {
		return ""
	}
	if !isGitHub(apiURL) {
		return ""
	}
	prefix := envName + "="
	for _, kv := range environ {
		if rest, ok := strings.CutPrefix(kv, prefix); ok {
			return rest
		}
	}
	return ""
}

// isGitHub reports whether the API URL is github.com's own.
func isGitHub(apiURL string) bool {
	if strings.TrimSpace(apiURL) == "" {
		return true // the default, which is api.github.com
	}
	u, err := url.Parse(apiURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "api.github.com" || host == "github.com"
}
