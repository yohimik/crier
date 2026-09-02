// Command gendocs writes the generated half of crier's documentation: the group
// pages under docs/configuration/ and the crier.example.yaml at the repository
// root.
//
// Both are generated rather than written because they are the pieces that go
// stale silently: a key added to registry.go and not to the docs is invisible
// until somebody needs it, and a sample config missing a key is a sample that
// teaches the wrong thing. The docs gate regenerates them and fails on a diff,
// so the code and the documentation cannot drift.
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yohimik/crier/internal/config"
	"github.com/yohimik/crier/internal/configgen"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	if err := run(root); err != nil {
		fmt.Fprintln(os.Stderr, "gendocs:", err)
		os.Exit(1)
	}
}

func run(root string) error {
	dir := filepath.Join(root, "docs", "configuration")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// What this run will write, keyed by path relative to dir. Collected first
	// so the cleanup below knows exactly what it owns.
	pages := map[string][]byte{}
	for _, g := range groups() {
		if len(g.keys) == 0 && g.prefix != config.CustomPrefix+"." {
			continue
		}
		if g.prefix == config.CustomPrefix+"." {
			// The custom platforms have no registry entries, since their names
			// belong to the operator, so their page comes from the leaf list.
			// It is a real page rather than a hole: those keys are as settable
			// as any other.
			pages[g.path] = renderCustom(g)
			continue
		}
		pages[g.path] = renderGroup(g)
	}

	if err := clean(dir, pages); err != nil {
		return err
	}
	for path, body := range pages {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, body, 0o644); err != nil {
			return err
		}
	}
	// The sample comes from internal/configgen rather than from here, because
	// `crier init --full` writes the same file from inside the binary. One
	// generator is what keeps the sample in the repository and the sample a
	// user is handed from saying different things.
	example, err := configgen.Sample(configgen.Options{
		Format: configgen.YAML,
		Full:   true,
		Header: generatedNote + "\n\n" + configgen.FullPreamble,
	})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "crier.example.yaml"), example, 0o644)
}

// generatedNote is the header every generated file carries. It lives in
// internal/configgen so the generated sample config carries the same words.
const generatedNote = configgen.GeneratedNote

// groupDirs are the folders under docs/configuration that belong entirely to
// this command. Everything inside one is generated, so a stale page is removed
// by clearing the folder.
var groupDirs = []string{"render", "stage", "publish"}

// clean removes what a previous run wrote and this one will not.
//
// The generated pages sit beside a hand-written README, so the whole directory
// cannot simply be cleared. The folders are wholly owned and are cleared
// outright. The single-page groups live at the top level, where a file is
// removed only if it carries the generated marker and is not about to be
// rewritten, which is what lets a group be renamed without leaving an orphan
// behind for the freshness gate to trip over.
func clean(dir string, pages map[string][]byte) error {
	for _, d := range groupDirs {
		if err := os.RemoveAll(filepath.Join(dir, d)); err != nil {
			return err
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		if _, keep := pages[e.Name()]; keep {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		if !bytes.HasPrefix(body, []byte("<!-- "+generatedNote)) {
			continue // hand-written, and none of this command's business
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// group is one page of the reference: a key prefix, the path it lands in
// relative to docs/configuration, and the keys that belong to it.
type group struct {
	prefix string
	path   string
	title  string
	intro  string
	keys   []string
}

// depth is how many folders deep the page sits under docs/configuration.
func (g group) depth() int { return strings.Count(g.path, "/") }

// toConfig is the relative prefix from this page back to docs/configuration.
func (g group) toConfig() string {
	if g.depth() == 0 {
		return "./"
	}
	return strings.Repeat("../", g.depth())
}

// toDocs and toRoot are the same idea, one and two levels further out.
func (g group) toDocs() string { return strings.Repeat("../", g.depth()+1) }
func (g group) toRoot() string { return strings.Repeat("../", g.depth()+2) }

// prefixes are the key paths the reference is split on, longest first so a key
// lands in the most specific group that claims it.
//
// The list is structural rather than editorial: every entry is a real prefix in
// the registry, and the per-platform ones are generated from the platform list
// so a new platform gets a page without anybody remembering to add one.
func prefixes() []group {
	out := []group{
		{prefix: "log.", path: "log.md", title: "Logging",
			intro: "Where the logs go, and how loud. Logs always go to standard error, so standard output stays a clean channel for results."},
		{prefix: "render.video.", path: "render/video.md", title: "Video rendering",
			intro: "Rendering an animated template into an MP4. ffmpeg does the encoding and is a prerequisite crier does not bundle."},
		{prefix: "render.", path: "render/README.md", title: "Rendering",
			intro: "What is drawn, how large, in which format, and with which fonts."},
		{prefix: "http.", path: "http.md", title: "HTTP",
			intro: "The shared client every publisher and stager uses: timeouts and retries.\n\n" +
				"There are two timeouts because there are two kinds of wait. `http.timeout` bounds an\n" +
				"ordinary API call, where a minute is generous. `http.upload-timeout` bounds a request\n" +
				"carrying media, where the same minute would not be enough to push a 50MB video up a\n" +
				"domestic uplink — and since a timeout bounds the body write as well as the response,\n" +
				"one setting for both means every large upload fails at a deterministic size. A request\n" +
				"whose body is over 1MB, or whose length is not known in advance because crier is\n" +
				"streaming it, gets the upload timeout."},
		{prefix: "stage.s3.", path: "stage/s3.md", title: "Staging: S3",
			intro: "Used when `stage.mode` is `s3`."},
		{prefix: "stage.server.tunnel.", path: "stage/tunnel.md", title: "Staging: tunnel",
			intro: "Used when `stage.mode` is `server` and the listener has to be reachable from the internet."},
		{prefix: "stage.server.", path: "stage/server.md", title: "Staging: local server",
			intro: "Used when `stage.mode` is `server`."},
		{prefix: "stage.", path: "stage/README.md", title: "Staging",
			intro: "How a rendered file is given a public URL, for the platforms that fetch rather than accept an upload."},
	}
	for _, p := range config.Platforms {
		out = append(out, group{
			prefix: "publish." + p + ".",
			path:   "publish/" + p + ".md",
			title:  "Publishing: " + platformTitle(p),
			intro:  "See [the " + platformTitle(p) + " guide](../../publishing/" + p + ".md) for how to get these values.",
		})
	}
	out = append(out, group{
		prefix: "publish.custom.", path: "publish/custom.md", title: "Publishing: custom platforms",
		intro: "Any shell command as a platform.",
	})
	return append(out, group{prefix: "publish.", path: "publish/README.md", title: "Publishing",
		intro: "The fan-out itself: which platforms, how many at a time, and the shared caption."})
}

func platformTitle(name string) string {
	switch name {
	case "tiktok":
		return "TikTok"
	case "x":
		return "X"
	case "linkedin":
		return "LinkedIn"
	case "slack":
		return "Slack"
	case "vk":
		return "VK"
	case "threads":
		return "Threads"
	default:
		return strings.ToUpper(name[:1]) + name[1:]
	}
}

// groups assigns every registry key to exactly one page.
func groups() []group {
	out := prefixes()
	assigned := map[string]bool{}
	for i := range out {
		for _, key := range config.Keys() {
			if assigned[key] || !strings.HasPrefix(key, out[i].prefix) {
				continue
			}
			out[i].keys = append(out[i].keys, key)
			assigned[key] = true
		}
		sort.Strings(out[i].keys)
	}
	// A key no prefix claims would be invisible, so it gets a page of its own
	// rather than being dropped.
	var rest []string
	for _, key := range config.Keys() {
		if !assigned[key] {
			rest = append(rest, key)
		}
	}
	if len(rest) > 0 {
		out = append(out, group{
			prefix: "", path: "other.md", title: "Other",
			intro: "Keys that belong to no group above.", keys: rest,
		})
	}
	return out
}

// renderCustom writes the reference page for a custom platform's keys.
func renderCustom(g group) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "<!-- %s -->\n\n", generatedNote)
	fmt.Fprintf(&b, "# %s\n\n", g.title)
	fmt.Fprintf(&b, `Any shell command can be a platform. The name is yours to choose, so these keys
are written with `+"`<name>`"+` standing in for it. See
[the custom platform guide](%spublishing/custom.md).

`, g.toDocs())
	fmt.Fprintln(&b, "| Key | Type | Default | Description |")
	fmt.Fprintln(&b, "| --- | ---- | ------- | ----------- |")
	for _, d := range config.CustomLeaves {
		full, _ := config.CustomDescriptor("<name>", d.Key)
		fmt.Fprintf(&b, "| `%s`<br>`%s`<br>`--set %s` | %s | %s | %s |\n",
			full.Key, config.EnvName(full.Key), full.Key, typeOf(d), defaultOf(d), describe(d))
	}
	fmt.Fprintf(&b, "| `%s.<name>.%s.<VAR>` | string | — | extra environment variables for the command |\n",
		config.CustomPrefix, config.CustomEnvLeaf)
	b.WriteString("\nA name is lower-case letters, digits and dashes, and may not be one of the\n" +
		"twelve built-in platforms. It has to survive the round trip through an\n" +
		"environment variable.\n")
	fmt.Fprintf(&b, "\n[All configuration](%sREADME.md)\n", g.toConfig())
	return b.Bytes()
}

func renderGroup(g group) []byte {
	byKey := config.Descriptors()
	var b bytes.Buffer
	fmt.Fprintf(&b, "<!-- %s -->\n\n", generatedNote)
	fmt.Fprintf(&b, "# %s\n\n", g.title)
	if g.intro != "" {
		fmt.Fprintf(&b, "%s\n\n", g.intro)
	}
	fmt.Fprintln(&b, "| Key | Type | Default | Description |")
	fmt.Fprintln(&b, "| --- | ---- | ------- | ----------- |")
	for _, key := range g.keys {
		d := byKey[key]
		fmt.Fprintf(&b, "| `%s`<br>`%s`<br>`--%s` | %s | %s | %s |\n",
			d.Key, d.EnvName(), d.FlagName(), typeOf(d), defaultOf(d), describe(d))
	}
	fmt.Fprintf(&b, "\nA sample carrying every key with its default is at\n"+
		"[`crier.example.yaml`](%scrier.example.yaml).\n", g.toRoot())
	fmt.Fprintf(&b, "\n[All configuration](%sREADME.md)\n", g.toConfig())
	return b.Bytes()
}

func typeOf(d config.Descriptor) string {
	if d.Path {
		return d.Kind.String() + ", path"
	}
	return d.Kind.String()
}

func defaultOf(d config.Descriptor) string {
	if d.Default == "" {
		return "—"
	}
	return "`" + d.Default + "`"
}

func describe(d config.Descriptor) string {
	out := strings.ReplaceAll(d.Usage, "|", `\|`)
	if d.Secret {
		out += " **Secret**: redacted by `crier config`."
	}
	return out
}
