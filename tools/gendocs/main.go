// Command gendocs writes the generated half of crier's documentation:
// docs/configuration/reference/ and the crier.example.yaml at the repository
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
	dir := filepath.Join(root, "docs", "configuration", "reference")
	// Removed first so a group that no longer exists does not survive as a
	// file nothing links to.
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	groups := groups()
	// The custom platforms have no registry entries — their names belong to
	// the operator — so their page is generated from the leaf list instead.
	// It is a real page rather than a hole in the reference: those keys are as
	// settable as any other.
	if err := os.WriteFile(filepath.Join(dir, "publish-custom.md"), renderCustom(), 0o644); err != nil {
		return err
	}
	for _, g := range groups {
		if len(g.keys) == 0 {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, g.file), renderGroup(g), 0o644); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), renderIndex(groups), 0o644); err != nil {
		return err
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

// group is one page of the reference: a key prefix, the file it lands in, and
// the keys that belong to it.
type group struct {
	prefix string
	file   string
	title  string
	intro  string
	keys   []string
}

// prefixes are the key paths the reference is split on, longest first so a key
// lands in the most specific group that claims it.
//
// The list is structural rather than editorial: every entry is a real prefix in
// the registry, and the per-platform ones are generated from the platform list
// so a new platform gets a page without anybody remembering to add one.
func prefixes() []group {
	out := []group{
		{prefix: "log.", title: "Logging",
			intro: "Where the logs go, and how loud. Logs always go to standard error, so standard output stays a clean channel for results."},
		{prefix: "render.video.", title: "Video rendering",
			intro: "Rendering an animated template into an MP4. ffmpeg does the encoding and is a prerequisite crier does not bundle."},
		{prefix: "render.", title: "Rendering",
			intro: "What is drawn, how large, in which format, and with which fonts."},
		{prefix: "http.", title: "HTTP",
			intro: "The shared client every publisher and stager uses: timeouts and retries.\n\n" +
				"There are two timeouts because there are two kinds of wait. `http.timeout` bounds an\n" +
				"ordinary API call, where a minute is generous. `http.upload-timeout` bounds a request\n" +
				"carrying media, where the same minute would not be enough to push a 50MB video up a\n" +
				"domestic uplink — and since a timeout bounds the body write as well as the response,\n" +
				"one setting for both means every large upload fails at a deterministic size. A request\n" +
				"whose body is over 1MB, or whose length is not known in advance because crier is\n" +
				"streaming it, gets the upload timeout."},
		{prefix: "stage.s3.", title: "Staging: S3",
			intro: "Used when `stage.mode` is `s3`."},
		{prefix: "stage.server.tunnel.", title: "Staging: tunnel",
			intro: "Used when `stage.mode` is `server` and the listener has to be reachable from the internet."},
		{prefix: "stage.server.", title: "Staging: local server",
			intro: "Used when `stage.mode` is `server`."},
		{prefix: "stage.", title: "Staging",
			intro: "How a rendered file is given a public URL, for the platforms that fetch rather than accept an upload."},
	}
	for _, p := range config.Platforms {
		out = append(out, group{
			prefix: "publish." + p + ".",
			title:  "Publishing: " + platformTitle(p),
			intro:  "See [the " + platformTitle(p) + " guide](../../publishing/" + p + ".md) for how to get these values.",
		})
	}
	out = append(out, group{
		prefix: "publish.custom.", file: "publish-custom.md", title: "Publishing: custom platforms",
		intro: "Any shell command as a platform.",
	})
	return append(out, group{prefix: "publish.", title: "Publishing",
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
	default:
		return strings.ToUpper(name[:1]) + name[1:]
	}
}

// fileFor turns a key prefix into a file name: dots become dashes.
func fileFor(prefix string) string {
	return strings.ReplaceAll(strings.TrimSuffix(prefix, "."), ".", "-") + ".md"
}

// groups assigns every registry key to exactly one page.
func groups() []group {
	out := prefixes()
	for i := range out {
		out[i].file = fileFor(out[i].prefix)
	}
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
			prefix: "", file: "other.md", title: "Other",
			intro: "Keys that belong to no group above.", keys: rest,
		})
	}
	return out
}

// renderCustom writes the reference page for a custom platform's keys.
func renderCustom() []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "<!-- %s -->\n\n", generatedNote)
	b.WriteString(`# Publishing: custom platforms

Any shell command can be a platform. The name is yours to choose, so these keys
are written with ` + "`<name>`" + ` standing in for it — see
[the custom platform guide](../../publishing/custom.md).

`)
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
		"nine built-in platforms: it has to survive the round trip through an\n" +
		"environment variable.\n")
	fmt.Fprintln(&b, "\n[All groups](./README.md)")
	return b.Bytes()
}

func renderIndex(gs []group) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "<!-- %s -->\n\n", generatedNote)
	b.WriteString(`# Configuration reference

Every key crier has, one page per group. How the three layers compose, and
where a relative path resolves against, is in
[the configuration guide](../README.md).

`)
	fmt.Fprintln(&b, "| Group | Keys | What it covers |")
	fmt.Fprintln(&b, "| ----- | ---- | -------------- |")
	for _, g := range gs {
		if len(g.keys) == 0 && g.file != "publish-custom.md" {
			continue
		}
		count := len(g.keys)
		if g.file == "publish-custom.md" {
			count = len(config.CustomLeaves) + 1
		}
		prefix := g.prefix
		if prefix == "" {
			prefix = "—"
		} else {
			prefix = "`" + strings.TrimSuffix(prefix, ".") + "`"
		}
		fmt.Fprintf(&b, "| [%s](./%s) | %s | %d |\n", g.title, g.file, prefix, count)
	}
	b.WriteString("\nA sample carrying every key with its default is at\n" +
		"[`crier.example.yaml`](../../../crier.example.yaml).\n")
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
	fmt.Fprintln(&b, "\n[All groups](./README.md)")
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
