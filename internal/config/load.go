package config

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	dispat "github.com/yohimik/dispat/pkg/config"
)

// ConfigFlag is the flag (and, upper-cased with the prefix, the environment
// variable) naming the configuration file.
const ConfigFlag = "config"

// DefaultFileNames are the configuration file names looked for while walking
// up from the working directory, in this order within one directory.
var DefaultFileNames = []string{"crier.yaml", "crier.yml", "crier.json", "crier.toml", ".crier.yaml"}

// Defaults returns a Config with every registry default applied.
//
// The defaults go through the very same setters the file, the environment and
// the flags go through, so a default can only be spelled in one place and can
// only mean what the key means.
func Defaults() Config {
	var cfg Config
	ApplyDefaults(&cfg)
	return cfg
}

// ApplyDefaults writes the registry defaults into cfg, leaving keys with no
// declared default at their zero value.
func ApplyDefaults(cfg *Config) {
	b := Bindings(cfg)
	for _, d := range registry {
		if d.Default == "" {
			continue
		}
		bind, ok := b[d.Key]
		if !ok {
			// Impossible in a build that passes the anti-drift tests; panicking
			// here would turn a schema bug into a crash for the user, so the
			// key is simply skipped.
			continue
		}
		_ = bind.Set(d.Default, d.Key)
	}
}

// SetFlag is the name of the universal override flag.
const SetFlag = "set"

// setList collects a repeatable --set key=value flag.
//
// It exists for the keys that cannot have a flag of their own: a custom
// platform's name is not known until the configuration has been read, so no
// flag can be registered for publish.custom.<name>.command ahead of time. It
// works for every other key too, which makes it a general escape hatch as well
// as the answer for the dynamic ones.
type setList struct{ pairs []string }

func (s *setList) String() string { return strings.Join(s.pairs, ",") }

func (s *setList) Set(v string) error {
	s.pairs = append(s.pairs, v)
	return nil
}

// Flags registers one command line flag per configuration key on a FlagSet and
// collects the ones the operator actually typed.
type Flags struct {
	fs      *flag.FlagSet
	strs    map[string]*string // flag name -> value
	bools   map[string]*bool   // flag name -> value
	keyOf   map[string]string  // flag name -> config key
	isAlias map[string]bool    // flag name -> declared as an alias
	confPtr *string
	sets    *setList
}

// RegisterFlags adds every configuration flag, plus --config, to fs.
//
// Booleans get a real boolean flag so that `--publish-dry-run` works without
// an argument; every other kind is a string flag, because the decoder is
// weakly typed and will coerce the text into the field's shape.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{
		fs:      fs,
		strs:    map[string]*string{},
		bools:   map[string]*bool{},
		keyOf:   map[string]string{},
		isAlias: map[string]bool{},
	}
	f.confPtr = fs.String(ConfigFlag, "", "path to a configuration file (json, yaml or toml)")
	f.sets = &setList{}
	fs.Var(f.sets, SetFlag, "override any configuration key: --set key=value, repeatable")

	byKey := Descriptors()
	for _, d := range registry {
		f.register(d.FlagName(), d, false)
	}
	names := make([]string, 0, len(Aliases))
	for name := range Aliases {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		d, ok := byKey[Aliases[name]]
		if !ok {
			continue
		}
		f.register(name, d, true)
	}
	return f
}

func (f *Flags) register(name string, d Descriptor, alias bool) {
	usage := d.Usage
	if alias {
		usage = fmt.Sprintf("%s (alias for --%s)", d.Usage, d.FlagName())
	}
	f.keyOf[name] = d.Key
	f.isAlias[name] = alias
	if d.Kind == KindBool {
		f.bools[name] = f.fs.Bool(name, false, usage)
		return
	}
	f.strs[name] = f.fs.String(name, "", usage)
}

// ConfigPath is the value of --config, empty when it was not given.
func (f *Flags) ConfigPath() string {
	if f.confPtr == nil {
		return ""
	}
	return *f.confPtr
}

// Overrides returns the values of the flags that were actually set, keyed by
// configuration key. A flag left alone contributes nothing, which is what
// keeps a flag's zero value from silently outranking the file.
//
// When both a key's own flag and one of its aliases are given, the key's own
// flag wins, and an explicit --set outranks both: it is the most specific
// thing the operator can have typed.
//
// A --set naming a key crier does not have is an error rather than a value
// quietly going nowhere. That is the same rule the configuration decoder
// applies to a file, and for the same reason: a setting that does nothing is
// worse than one that fails, because it looks like it worked.
func (f *Flags) Overrides() (dispat.Overrides, error) {
	var aliasSet, exactSet []string
	f.fs.Visit(func(fl *flag.Flag) {
		if _, ok := f.keyOf[fl.Name]; !ok {
			return
		}
		if f.isAlias[fl.Name] {
			aliasSet = append(aliasSet, fl.Name)
			return
		}
		exactSet = append(exactSet, fl.Name)
	})
	out := dispat.Overrides{}
	for _, name := range append(aliasSet, exactSet...) {
		key := f.keyOf[name]
		if p, ok := f.bools[name]; ok {
			out[key] = strconv.FormatBool(*p)
			continue
		}
		out[key] = *f.strs[name]
	}
	if f.sets != nil {
		for _, pair := range f.sets.pairs {
			key, value, ok := strings.Cut(pair, "=")
			if !ok {
				return nil, fmt.Errorf("--%s %q is not key=value", SetFlag, pair)
			}
			key = strings.TrimSpace(key)
			if err := CheckKey(key); err != nil {
				return nil, fmt.Errorf("--%s: %w", SetFlag, err)
			}
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// EnvBinding is the closed set of environment variables crier reads.
//
// Closed is the point: a CRIER_ variable crier has no key for is a typo, and a
// binding that accepted anything would swallow it. extra carries the keys of
// the custom platforms, which cannot be in the registry because their names
// are not crier's to choose.
func EnvBinding(environ []string, extra ...string) dispat.EnvBinding {
	keys := Keys()
	if len(extra) > 0 {
		keys = append(append([]string(nil), keys...), extra...)
		sort.Strings(keys)
	}
	return dispat.EnvBinding{
		Prefix:   EnvPrefix,
		Keys:     keys,
		Environ:  environ,
		KeyDelim: dispat.DefaultKeyDelim,
	}
}

// Options configures Load.
type Options struct {
	// Path is the configuration file named on the command line. When empty,
	// CRIER_CONFIG and then the ascent from Dir are tried.
	Path string
	// Environ is the process environment as KEY=value pairs. A nil slice means
	// os.Environ(); a non-nil empty slice means an empty environment.
	Environ []string
	// FlagOverrides are the values the operator typed, which outrank both the
	// file and the environment.
	FlagOverrides dispat.Overrides
	// Dir is where the search for a configuration file starts. Empty means the
	// process working directory.
	Dir string
}

// Result is what Load produces: the configuration and where it came from.
type Result struct {
	Config Config
	// File is the configuration file that was read, empty when none was found.
	File string
	// Dir is the directory the configuration file sits in — the project root.
	// Relative paths written in the file are resolved against it.
	Dir string
	// Files are every file that was read, including the ones pulled in by
	// `$ref`.
	Files []string
}

// Load composes the three layers into a Config: registry defaults underneath,
// then the file, then the environment, then the flags.
//
// The file is found by walking up from the working directory, the way git
// finds a repository, so running crier inside one project uses that project's
// configuration and running it inside another uses the other's. An explicitly
// named file skips the search; a file that was named and does not exist is an
// error, while finding nothing on the way up is not.
func Load(ctx context.Context, o Options) (*Result, error) {
	cfg := Defaults()

	environ := o.Environ
	if environ == nil {
		environ = os.Environ()
	}
	dir := o.Dir
	if dir == "" {
		dir = "."
	}

	loader := dispat.NewLoader(dispat.Options{})
	path := explicitPath(o, environ)
	if path == "" {
		found, _, err := loader.Resolve(ctx, dir, dispat.Resolver{Names: DefaultFileNames})
		if err != nil && !errors.Is(err, dispat.ErrNoConfig) {
			return nil, err
		}
		path = found
	}

	tree := &dispat.Tree{Root: map[string]any{}}
	configDir := ""
	if path != "" {
		read, err := loader.ReadTree(ctx, path)
		if err != nil {
			// A config file that was named, or that the ascent found, and that
			// cannot be read is broken rather than absent: stepping over it
			// would silently run with somebody else's settings.
			return nil, fmt.Errorf("reading config %s: %w", path, err)
		}
		tree = read
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		configDir = filepath.Dir(abs)
	}

	fileSettings := tree.Settings(loader, nil)

	// The custom platforms have to be discovered before the environment is
	// read, because the environment binding is a closed list of keys and their
	// names are not in it. A name may be introduced by any of the three
	// layers: the file is the natural home, the environment is where CI puts
	// one, and --set is the only way a flag can name a key nobody declared.
	names := mergeNames(
		CustomNamesInTree(fileSettings),
		CustomNamesInEnv(environ),
		CustomNamesInOverrides(o.FlagOverrides),
	)

	envOverrides, err := EnvBinding(environ, CustomEnvKeys(names)...).Overrides(ctx)
	if err != nil {
		return nil, err
	}

	// An entry per discovered name exists before the decode, so a platform
	// named only in the environment or by --set is a platform rather than a
	// set of values with nowhere to go.
	for _, name := range names {
		cfg.Publish.ensureCustom(name)
	}

	settings := tree.Settings(loader, dispat.MergeOverrides(envOverrides, o.FlagOverrides))
	if err := dispat.DecodeObject(settings, "", Fields(&cfg)); err != nil {
		return nil, err
	}

	if configDir != "" {
		anchorPaths(&cfg, configDir, fileSettings, envOverrides, o.FlagOverrides)
	}

	res := &Result{Config: cfg, Files: tree.Files}
	if path != "" {
		res.File = path
		res.Dir = configDir
	}
	return res, nil
}

// mergeNames unions the custom platform names the three layers mention.
func mergeNames(lists ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range lists {
		for _, name := range list {
			if seen[name] || CheckCustomName(name) != nil {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// explicitPath is the configuration file the operator named, empty when they
// named none and the ascent has to find one.
func explicitPath(o Options, environ []string) string {
	if o.Path != "" {
		return o.Path
	}
	if v, ok := lookupEnv(environ, EnvPrefix+"CONFIG"); ok && v != "" {
		return v
	}
	return ""
}

// anchorPaths resolves the relative file names a config file wrote against
// that file's own directory.
//
// A project's config sits next to its template, and `template: story.html`
// there has to mean the file beside it however deep in the tree crier was run
// from. A path given on the command line or in the environment is left alone,
// because it was typed where the shell is and means what it says there.
func anchorPaths(cfg *Config, dir string, fileSettings map[string]any, env, flags dispat.Overrides) {
	b := Bindings(cfg)
	// The custom platforms' path keys are anchored the same way. They are
	// appended rather than special-cased, because a path written in a config
	// file means the same thing wherever in the file it was written.
	descriptors := Registry()
	for _, name := range CustomNames(&cfg.Publish) {
		for _, leaf := range CustomLeaves {
			d, _ := CustomDescriptor(name, leaf.Key)
			descriptors = append(descriptors, d)
		}
	}
	for _, d := range descriptors {
		if !d.Path {
			continue
		}
		if _, set := flags[d.Key]; set {
			continue
		}
		if _, set := env[d.Key]; set {
			continue
		}
		if !settingsHave(fileSettings, d.Key) {
			continue
		}
		bind, ok := b[d.Key]
		if !ok {
			continue
		}
		switch v := bind.Get().(type) {
		case string:
			_ = bind.Set(anchorOne(dir, v), d.Key)
		case []string:
			out := make([]string, len(v))
			for i, item := range v {
				out[i] = anchorOne(dir, item)
			}
			_ = bind.Set(out, d.Key)
		}
	}
}

// anchorOne resolves one path against dir, leaving alone what must not move:
// an empty value, an absolute path, and the "-" that means standard input.
func anchorOne(dir, value string) string {
	if value == "" || value == "-" || filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(dir, value)
}

// settingsHave reports whether a rendered settings map holds a value at a
// dotted key, matching every level case-insensitively the way the decoder
// does.
func settingsHave(settings map[string]any, key string) bool {
	node := settings
	segs := strings.Split(key, dispat.DefaultKeyDelim)
	for i, seg := range segs {
		_, value, ok := dispat.LookupFold(node, seg)
		if !ok {
			return false
		}
		if i == len(segs)-1 {
			return value != nil
		}
		child, isObject := value.(map[string]any)
		if !isObject {
			return false
		}
		node = child
	}
	return false
}

// lookupEnv finds a variable in an explicit environment slice. The last
// occurrence wins, matching the way a process environment is read.
func lookupEnv(environ []string, name string) (string, bool) {
	prefix := name + "="
	value, found := "", false
	for _, pair := range environ {
		if strings.HasPrefix(pair, prefix) {
			value, found = pair[len(prefix):], true
		}
	}
	return value, found
}
