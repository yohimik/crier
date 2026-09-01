package config

import (
	"fmt"
	"sort"
	"strings"

	dispat "github.com/yohimik/dispat/pkg/config"
)

// CustomPrefix is where script-backed platforms live in the configuration.
const CustomPrefix = "publish.custom"

// CustomLeaves are the keys one custom platform has, in the order the
// documentation lists them.
//
// The Key of each is the leaf alone; CustomDescriptor turns one into the full
// dotted key for a named platform. Keeping the list here rather than beside the
// registry is what lets both the docs and --set describe a key whose middle
// segment nobody has written down yet.
var CustomLeaves = []Descriptor{
	{Key: "enabled", Kind: KindBool, Usage: "publish through this custom platform"},
	{Key: "command", Kind: KindString, Usage: "shell command that publishes the artifact"},
	{Key: "ping-command", Kind: KindString, Usage: "shell command `crier ping` runs to check the credentials"},
	{Key: "caption", Kind: KindString, Usage: "caption for this platform, overriding publish.caption"},
	{Key: "kinds", Kind: KindStrings, Default: "image", Usage: "artifact kinds the command accepts: image, video, gif"},
	{Key: "format", Kind: KindString, Default: "png", Usage: "preferred image format: png or jpeg"},
	{Key: "needs-url", Kind: KindBool, Usage: "stage the artifact and pass CRIER_URL"},
	{Key: "timeout", Kind: KindString, Default: "2m", Usage: "how long the command may run"},
	{Key: "overlay", Kind: KindStrings, Path: true, Usage: "extra template overlays for this platform"},
	{Key: "width", Kind: KindInt, Usage: "render width for this platform"},
	{Key: "height", Kind: KindInt, Usage: "render height for this platform"},
	{Key: "fit", Kind: KindString, Default: "none", Usage: "how the render is made to match this platform's frame: none, cover, contain or stretch"},
	{Key: "fit-background", Kind: KindString, Default: "#ffffff", Usage: "hex colour behind a contain letterbox, and what transparency is flattened onto"},
}

// CustomEnvLeaf is the sub-object holding extra environment variables. It is
// the second dynamic level, and the only one.
const CustomEnvLeaf = "env"

// CustomDescriptor is one leaf of one named custom platform, as a descriptor
// indistinguishable from a static one.
func CustomDescriptor(name, leaf string) (Descriptor, bool) {
	for _, d := range CustomLeaves {
		if d.Key == leaf {
			d.Key = CustomPrefix + "." + name + "." + leaf
			return d, true
		}
	}
	return Descriptor{}, false
}

// CustomKeys are every key a named custom platform has, sorted.
func CustomKeys(name string) []string {
	out := make([]string, 0, len(CustomLeaves))
	for _, d := range CustomLeaves {
		out = append(out, CustomPrefix+"."+name+"."+d.Key)
	}
	sort.Strings(out)
	return out
}

// CustomNames are the script-backed platforms a configuration declares,
// sorted.
func CustomNames(p *Publish) []string {
	out := make([]string, 0, len(p.Custom))
	for name := range p.Custom {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// CustomOf is one named custom platform, or nil.
func CustomOf(p *Publish, name string) *Custom {
	if p.Custom == nil {
		return nil
	}
	return p.Custom[name]
}

// ensureCustom returns the entry for a name, creating it with its defaults.
func (p *Publish) ensureCustom(name string) *Custom {
	if p.Custom == nil {
		p.Custom = map[string]*Custom{}
	}
	c, ok := p.Custom[name]
	if !ok {
		c = &Custom{}
		p.Custom[name] = c
		applyCustomDefaults(c)
	}
	return c
}

// applyCustomDefaults writes the leaf defaults into a fresh entry, through the
// same setters everything else goes through.
func applyCustomDefaults(c *Custom) {
	b := customBindings(c)
	for _, d := range CustomLeaves {
		if d.Default == "" {
			continue
		}
		if bind, ok := b[d.Key]; ok {
			_ = bind.Set(d.Default, d.Key)
		}
	}
}

// customBindings maps one entry's leaf names to its fields.
func customBindings(c *Custom) map[string]Binding {
	return map[string]Binding{
		"enabled":        bindBool(&c.Enabled),
		"command":        bindString(&c.Command),
		"ping-command":   bindString(&c.PingCommand),
		"caption":        bindString(&c.Caption),
		"kinds":          bindStrings(&c.Kinds),
		"format":         bindString(&c.Format),
		"needs-url":      bindBool(&c.NeedsURL),
		"timeout":        bindString(&c.Timeout),
		"overlay":        bindStrings(&c.Layout.Overlay),
		"width":          bindInt(&c.Layout.Width),
		"height":         bindInt(&c.Layout.Height),
		"fit":            bindString(&c.Layout.Fit),
		"fit-background": bindString(&c.Layout.FitBackground),
	}
}

// customFields is the decode table for one entry, including the env map.
func customFields(c *Custom) dispat.Fields {
	out := dispat.Fields{}
	for leaf, bind := range customBindings(c) {
		out[strings.ToLower(leaf)] = bind.Set
	}
	out[CustomEnvLeaf] = bindStringMap(&c.Env).Set
	return out
}

// customSetter decodes the whole publish.custom object, creating an entry per
// name it finds.
//
// It is the one dynamic table in the configuration, and it still refuses an
// unknown key inside an entry: the names are open, the keys under them are not.
func customSetter(cfg *Config) dispat.Setter {
	return func(val any, at string) error {
		obj, ok := val.(map[string]any)
		if !ok {
			return dispat.Wants(at, "an object of custom platforms")
		}
		for _, raw := range dispat.SortedKeys(obj) {
			name := strings.ToLower(strings.TrimSpace(raw))
			if err := CheckCustomName(name); err != nil {
				return fmt.Errorf("%s: %w", dispat.KeyPath(at, raw), err)
			}
			if obj[raw] == nil {
				continue
			}
			c := cfg.Publish.ensureCustom(name)
			if err := dispat.DecodeObject(obj[raw], dispat.KeyPath(at, raw), customFields(c)); err != nil {
				return err
			}
		}
		return nil
	}
}

// CheckCustomName refuses a name that would not survive the round trip through
// an environment variable or a flag.
//
// CRIER_PUBLISH_CUSTOM_MY_HOOK_COMMAND has to mean one thing, so a name is
// lower-case letters, digits and dashes: "my-hook" and "my_hook" would
// otherwise be the same variable and different platforms.
func CheckCustomName(name string) error {
	if name == "" {
		return fmt.Errorf("a custom platform needs a name")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			return fmt.Errorf("the name %q is not lower-case letters, digits and dashes", name)
		}
	}
	for _, builtin := range Platforms {
		if name == builtin {
			return fmt.Errorf("%q is already a built-in platform", name)
		}
	}
	return nil
}

// bindStringMap binds a map whose keys the configuration chooses.
//
// The keys are kept exactly as written, unlike everywhere else: these become
// environment variable names for the script, and a shell cares about their
// case.
func bindStringMap(p *map[string]string) Binding {
	return Binding{
		Set: func(val any, at string) error {
			obj, ok := val.(map[string]any)
			if !ok {
				return dispat.Wants(at, "an object of environment variables")
			}
			out := make(map[string]string, len(obj))
			for _, key := range dispat.SortedKeys(obj) {
				if obj[key] == nil {
					continue
				}
				out[key] = fmt.Sprint(obj[key])
			}
			*p = out
			return nil
		},
		Get: func() any {
			out := make(map[string]string, len(*p))
			for k, v := range *p {
				out[k] = v
			}
			return out
		},
	}
}

// checkCustomKey validates a key under publish.custom, and reports whether it
// was one at all.
//
// The names are open, so no list can be consulted; the shape is not, so the
// name and the leaf are both checked.
func checkCustomKey(key string) (err error, isCustom bool) {
	rest, ok := strings.CutPrefix(key, CustomPrefix+".")
	if !ok {
		return nil, false
	}
	name, leaf, ok := strings.Cut(rest, ".")
	if !ok {
		return fmt.Errorf("%s names a custom platform but no key under it; try %s.%s.command",
			key, CustomPrefix, name), true
	}
	if err := CheckCustomName(name); err != nil {
		return fmt.Errorf("%s: %w", key, err), true
	}
	// The env sub-object's keys are the operator's to choose.
	if envKey, ok := strings.CutPrefix(leaf, CustomEnvLeaf+"."); ok {
		if envKey == "" {
			return fmt.Errorf("%s: an environment variable needs a name", key), true
		}
		return nil, true
	}
	if _, ok := CustomDescriptor(name, leaf); !ok {
		return fmt.Errorf("unknown key %q; a custom platform has: %s",
			key, strings.Join(customLeafNames(), ", ")), true
	}
	return nil, true
}

func customLeafNames() []string {
	out := make([]string, 0, len(CustomLeaves)+1)
	for _, d := range CustomLeaves {
		out = append(out, d.Key)
	}
	out = append(out, CustomEnvLeaf+".<NAME>")
	sort.Strings(out)
	return out
}

// CustomEnvKeys are the environment-variable keys the loader has to bind for a
// set of discovered custom names.
//
// The environment binding is a closed list of keys — that is what makes an
// unknown CRIER_ variable a mistake rather than a silent no-op — so the names
// found in the file have to be turned into keys before the environment is
// read. That is why this exists and why Load reads the file first.
func CustomEnvKeys(names []string) []string {
	var out []string
	for _, name := range names {
		out = append(out, CustomKeys(name)...)
	}
	sort.Strings(out)
	return out
}

// CustomNamesInTree finds the platforms a settings tree declares.
func CustomNamesInTree(settings map[string]any) []string {
	node := settings
	for _, seg := range strings.Split(CustomPrefix, ".") {
		_, value, ok := dispat.LookupFold(node, seg)
		if !ok {
			return nil
		}
		child, isObject := value.(map[string]any)
		if !isObject {
			return nil
		}
		node = child
	}
	out := make([]string, 0, len(node))
	for name := range node {
		out = append(out, strings.ToLower(strings.TrimSpace(name)))
	}
	sort.Strings(out)
	return out
}

// CustomNamesInEnv finds the platforms only the environment mentions.
//
// A name can be introduced anywhere a value can, or "settable three ways"
// would be untrue for the one part of the configuration whose keys are not
// crier's to choose. The leaf suffix is what resolves the ambiguity of a name
// containing the same underscore the delimiter uses: the longest leaf that
// matches wins, and what precedes it is the name.
func CustomNamesInEnv(environ []string) []string {
	prefix := EnvPrefix + strings.ToUpper(strings.NewReplacer(".", "_", "-", "_").Replace(CustomPrefix)) + "_"
	seen := map[string]bool{}
	for _, pair := range environ {
		name, _, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		rest, ok := strings.CutPrefix(name, prefix)
		if !ok {
			continue
		}
		if found := customNameOf(rest); found != "" {
			seen[found] = true
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// customNameOf strips a known leaf suffix off an environment variable's tail
// and returns the platform name, lower-cased and with underscores read as the
// dashes a name is written with.
func customNameOf(rest string) string {
	best := ""
	for _, d := range CustomLeaves {
		suffix := "_" + strings.ToUpper(strings.ReplaceAll(d.Key, "-", "_"))
		head, ok := strings.CutSuffix(rest, suffix)
		if !ok || head == "" {
			continue
		}
		// The longest match wins: PING_COMMAND and COMMAND both end a variable
		// that sets a ping command, and only one of them leaves a real name.
		if len(suffix) > len(best) {
			best = suffix
		}
	}
	if best == "" {
		// The env sub-object: CRIER_PUBLISH_CUSTOM_<NAME>_ENV_<VAR>.
		if head, _, ok := strings.Cut(rest, "_"+strings.ToUpper(CustomEnvLeaf)+"_"); ok && head != "" {
			return normaliseCustomName(head)
		}
		return ""
	}
	head := strings.TrimSuffix(rest, best)
	return normaliseCustomName(head)
}

func normaliseCustomName(head string) string {
	name := strings.ToLower(strings.ReplaceAll(head, "_", "-"))
	if CheckCustomName(name) != nil {
		return ""
	}
	return name
}

// CustomNamesInOverrides finds the platforms a --set introduced.
func CustomNamesInOverrides(over dispat.Overrides) []string {
	seen := map[string]bool{}
	for key := range over {
		rest, ok := strings.CutPrefix(key, CustomPrefix+".")
		if !ok {
			continue
		}
		if name, _, ok := strings.Cut(rest, "."); ok {
			name = strings.ToLower(strings.TrimSpace(name))
			if CheckCustomName(name) == nil {
				seen[name] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
