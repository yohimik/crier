package publish

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/procutil"
	"github.com/yohimik/crier/internal/render"
)

// Custom is a platform defined by a shell command.
//
// It is the escape hatch that keeps crier from needing a pull request per
// service: anything with an HTTP client and a shell can be a platform, and it
// is a peer of the ten built-ins rather than a lesser thing — same fan-out,
// same variants, same caption templating, same ping.
//
// The contract is deliberately small: the command is told where the file is
// and what to say, and it answers by writing to a file crier reads back. A
// script that only exits 0 is a valid publisher; one that reports an id and a
// link is a better one.
type Custom struct {
	name string
	cfg  *config.Custom
	dir  string
	log  zerolog.Logger
}

// Environment variables a custom command is given. They are the whole
// interface, so they are named here rather than spelled out at the call site.
const (
	// EnvPlatform is the name the configuration gave this platform.
	EnvPlatform = "CRIER_PLATFORM"
	// EnvArtifact is the file to publish. When a post carries several, it is
	// the first of them.
	EnvArtifact = "CRIER_ARTIFACT"
	// EnvArtifacts is every file this post carries, one per line, in page
	// order. It always holds at least the one CRIER_ARTIFACT names, so a
	// script can read this alone and never look at the singular form.
	//
	// One per line rather than space separated, because a path may contain a
	// space and a page list must not become a different list on the way in.
	EnvArtifacts = "CRIER_ARTIFACTS"
	// EnvArtifactCount is how many files this post carries.
	EnvArtifactCount = "CRIER_ARTIFACT_COUNT"
	// EnvArtifactFormat is png, jpeg, or empty for a video.
	EnvArtifactFormat = "CRIER_ARTIFACT_FORMAT"
	// EnvArtifactKind is image or video.
	EnvArtifactKind = "CRIER_ARTIFACT_KIND"
	// EnvArtifactType is the artifact's MIME type.
	EnvArtifactType = "CRIER_ARTIFACT_TYPE"
	// EnvURL is where the artifact was staged, set only when needs-url is on.
	EnvURL = "CRIER_URL"
	// EnvURLs is where each of this post's files was staged, one per line, in
	// the same order as CRIER_ARTIFACTS. Set only when needs-url is on.
	EnvURLs = "CRIER_URLS"
	// EnvCaption is the rendered post text.
	EnvCaption = "CRIER_CAPTION"
	// EnvPoster is a still image accompanying a video, when there is one.
	EnvPoster = "CRIER_POSTER"
	// EnvOutput is a file the command may append `id=` and `link=` lines to.
	EnvOutput = "CRIER_OUTPUT"
	// The paging of a run whose page list was longer than one post. They read
	// 1 of 1 when nothing paginated, so a script can use them either way.
	EnvPost  = "CRIER_POST"
	EnvPosts = "CRIER_POSTS"
	EnvPage  = "CRIER_PAGE"
	EnvPages = "CRIER_PAGES"
	// EnvProjectDir is the directory the configuration file sits in, which is
	// also the command's working directory.
	EnvProjectDir = "CRIER_PROJECT_DIR"
)

// DefaultCustomTimeout is how long a command may run when the configuration
// says nothing.
const DefaultCustomTimeout = 2 * time.Minute

// newCustom builds one script-backed publisher.
func newCustom(name string, c *config.Custom, d Deps) (Publisher, error) {
	if err := require(c.Command, config.CustomPrefix+"."+name+".command"); err != nil {
		return nil, err
	}
	if _, err := shell(); err != nil {
		return nil, err
	}
	return &Custom{name: name, cfg: c, dir: d.Dir, log: d.Logger}, nil
}

// Name implements Publisher.
func (c *Custom) Name() string { return c.name }

// Needs implements Publisher, from what the entry declared.
func (c *Custom) Needs() Needs {
	// A custom platform's capacity is whatever its configuration says, because
	// there is no API here to know better: the command is the platform.
	n := Needs{URL: c.cfg.NeedsURL, MaxAttachments: c.cfg.Layout.MaxAttachments}
	switch strings.ToLower(strings.TrimSpace(c.cfg.Format)) {
	case "jpeg", "jpg":
		n.Formats = []config.Format{config.JPEG, config.PNG}
	default:
		n.Formats = []config.Format{config.PNG, config.JPEG}
	}
	for _, kind := range c.cfg.Kinds {
		switch strings.ToLower(strings.TrimSpace(kind)) {
		case "image":
			n.Kinds = append(n.Kinds, render.KindImage)
		case "video":
			n.Kinds = append(n.Kinds, render.KindVideo)
		case "gif":
			n.Kinds = append(n.Kinds, render.KindGIF)
		}
		// An unrecognised word is not dropped here: config validation rejects
		// it, so reaching this point with one is impossible. Dropping it
		// silently is what made every enabled custom platform block a
		// render.video.format=gif run with no way to opt in.
	}
	if len(n.Kinds) == 0 {
		n.Kinds = imageOnly
	}
	return n
}

// Publish runs the command and reads back what it says it published.
func (c *Custom) Publish(ctx context.Context, in Input) (Result, error) {
	out, err := os.CreateTemp("", "crier-custom-*.txt")
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", c.name, err)
	}
	outPath := out.Name()
	_ = out.Close()
	defer func() { _ = os.Remove(outPath) }()

	arts := in.Sequence()
	paths := make([]string, len(arts))
	for i, a := range arts {
		paths[i] = a.Path
	}
	env := []string{
		EnvPlatform + "=" + c.name,
		EnvArtifact + "=" + in.Artifact.Path,
		EnvArtifacts + "=" + strings.Join(paths, "\n"),
		EnvArtifactCount + "=" + strconv.Itoa(len(paths)),
		EnvProjectDir + "=" + c.dir,
		EnvArtifactFormat + "=" + string(in.Artifact.Format),
		EnvArtifactKind + "=" + string(in.Artifact.Kind),
		EnvArtifactType + "=" + in.Artifact.ContentType,
		EnvCaption + "=" + in.Caption,
		EnvOutput + "=" + outPath,
		EnvPost + "=" + strconv.Itoa(orOne(in.Post)),
		EnvPosts + "=" + strconv.Itoa(orOne(in.Posts)),
		EnvPage + "=" + strconv.Itoa(orOne(in.Page)),
		EnvPages + "=" + strconv.Itoa(orOne(in.Pages)),
	}
	// CRIER_URL is set only when the platform asked to be staged, so a script
	// can tell "no URL was needed" from "staging produced nothing".
	if c.cfg.NeedsURL {
		env = append(env,
			EnvURL+"="+in.URL,
			EnvURLs+"="+strings.Join(in.SequenceURLs(), "\n"))
	}
	if in.Poster != nil {
		env = append(env, EnvPoster+"="+in.Poster.Path)
	}

	if err := c.run(ctx, c.cfg.Command, env, "publishing"); err != nil {
		return Result{}, err
	}
	return readCustomOutput(outPath)
}

// Ping runs the entry's ping-command.
//
// It never falls back to Command: the whole promise of ping is that it does not
// post, and a command written to publish would do exactly that.
func (c *Custom) Ping(ctx context.Context) (Identity, error) {
	command := strings.TrimSpace(c.cfg.PingCommand)
	if command == "" {
		return Identity{
			ID:   c.name,
			Note: "no ping-command is set, so there is nothing to check without publishing",
		}, nil
	}

	out, err := os.CreateTemp("", "crier-custom-ping-*.txt")
	if err != nil {
		return Identity{}, fmt.Errorf("%s: %w", c.name, err)
	}
	outPath := out.Name()
	_ = out.Close()
	defer func() { _ = os.Remove(outPath) }()

	env := []string{
		EnvPlatform + "=" + c.name,
		EnvProjectDir + "=" + c.dir,
		EnvOutput + "=" + outPath,
	}
	if err := c.run(ctx, command, env, "checking credentials"); err != nil {
		return Identity{}, err
	}
	res, err := readCustomOutput(outPath)
	if err != nil {
		return Identity{}, err
	}
	id := Identity{ID: firstNonEmpty(res.ID, c.name), Name: res.Extra["name"]}
	return id, nil
}

// run executes one command with the given environment on top of crier's own.
func (c *Custom) run(ctx context.Context, command string, env []string, what string) error {
	sh, err := shell()
	if err != nil {
		return err
	}

	timeout := config.Duration(c.cfg.Timeout)
	if timeout <= 0 {
		timeout = DefaultCustomTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	full := append(os.Environ(), env...)
	// The entry's own variables go last, so a configuration can override
	// anything, including one of crier's own.
	for _, key := range sortedKeys(c.cfg.Env) {
		full = append(full, key+"="+c.cfg.Env[key])
	}

	log := c.log.With().Str("platform", c.name).Logger()
	log.Debug().Str("command", command).Msg(what)

	proc, err := procutil.Start(ctx, procutil.Options{
		Name: c.name,
		Bin:  sh,
		Args: []string{"-c", command},
		// The project root, so `sh ./publish.sh` means the script beside the
		// configuration rather than one in whatever directory crier was run
		// from. Empty when there is no configuration file, which leaves the
		// command in crier's own directory.
		Dir:    c.dir,
		Env:    full,
		Logger: log,
	})
	if err != nil {
		return fmt.Errorf("%s: %w", c.name, err)
	}
	if err := proc.Wait(); err != nil {
		tail := strings.TrimSpace(proc.Tail())
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%s: the command did not finish within %s%s", c.name, timeout, withTail(tail))
		}
		return fmt.Errorf("%s: the command failed: %w%s", c.name, err, withTail(tail))
	}
	return nil
}

// withTail appends the last of a failed command's output, which is the only
// thing that says why it failed.
func withTail(tail string) string {
	if tail == "" {
		return ""
	}
	return "\n" + tail
}

// readCustomOutput parses the file the command was given.
//
// The format is the one dispat uses for its own scripts: `key=value` a line at
// a time, appended rather than written, so a script can add to it as it goes.
// Anything crier does not recognise is kept in Extra rather than dropped: a
// script reporting something crier has no field for is reporting it to whoever
// reads the JSON.
func readCustomOutput(path string) (Result, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{}, nil
		}
		return Result{}, err
	}
	defer func() { _ = f.Close() }()

	res := Result{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 8<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value)
		switch key {
		case "id":
			res.ID = value
		case "link", "url":
			res.URL = value
		default:
			if res.Extra == nil {
				res.Extra = map[string]string{}
			}
			res.Extra[key] = value
		}
	}
	if err := sc.Err(); err != nil {
		return Result{}, fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}
	return res, nil
}

// shell finds the interpreter a custom command runs under.
//
// sh rather than the platform's own shell, everywhere, so one command works on
// every machine crier runs on. On Windows that means Git Bash or WSL has to be
// on PATH, which is worth saying plainly rather than failing with "file not
// found" halfway through a publish.
func shell() (string, error) {
	path, err := procutil.LookPath("sh")
	if err == nil {
		return path, nil
	}
	if runtime.GOOS == "windows" {
		return "", fmt.Errorf("a custom platform runs its command with `sh`, and there is none on PATH; " +
			"install Git for Windows or WSL, or point PATH at an sh.exe")
	}
	return "", fmt.Errorf("a custom platform runs its command with `sh`, and there is none on PATH: %w", err)
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// orOne reads an unset counter as one, so a script sees "1 of 1" rather than
// "0 of 0" on a run that never paginated.
func orOne(n int) int {
	if n < 1 {
		return 1
	}
	return n
}
