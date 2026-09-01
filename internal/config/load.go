package config

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
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

// DefaultFileNames are looked for in the working directory when no
// configuration file was named explicitly, in this order.
var DefaultFileNames = []string{"crier.yaml", "crier.yml", "crier.json", "crier.toml"}

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

// Flags registers one command line flag per configuration key on a FlagSet and
// collects the ones the operator actually typed.
type Flags struct {
	fs      *flag.FlagSet
	strs    map[string]*string // flag name -> value
	bools   map[string]*bool   // flag name -> value
	keyOf   map[string]string  // flag name -> config key
	isAlias map[string]bool    // flag name -> declared as an alias
	confPtr *string
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
// flag wins.
func (f *Flags) Overrides() dispat.Overrides {
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
	if len(out) == 0 {
		return nil
	}
	return out
}

// EnvBinding is the closed set of environment variables crier reads.
func EnvBinding(environ []string) dispat.EnvBinding {
	return dispat.EnvBinding{
		Prefix:   EnvPrefix,
		Keys:     Keys(),
		Environ:  environ,
		KeyDelim: dispat.DefaultKeyDelim,
	}
}

// Options configures Load.
type Options struct {
	// Path is the configuration file named on the command line. When empty,
	// CRIER_CONFIG and then the default file names are tried.
	Path string
	// Environ is the process environment as KEY=value pairs. A nil slice means
	// os.Environ(); a non-nil empty slice means an empty environment.
	Environ []string
	// FlagOverrides are the values the operator typed, which outrank both the
	// file and the environment.
	FlagOverrides dispat.Overrides
	// Dir is the directory the default file names are looked for in. Empty
	// means the process working directory.
	Dir string
}

// Result is what Load produces: the configuration and where it came from.
type Result struct {
	Config Config
	// File is the configuration file that was read, empty when none was found.
	File string
	// Files are every file that was read, including the ones pulled in by
	// `$ref`.
	Files []string
}

// Load composes the three layers into a Config: registry defaults underneath,
// then the file, then the environment, then the flags.
//
// An explicitly named file that does not exist is an error; the default file
// names are only tried when no name was given, and their absence is not.
func Load(ctx context.Context, o Options) (*Result, error) {
	cfg := Defaults()

	environ := o.Environ
	if environ == nil {
		environ = os.Environ()
	}

	path, explicit, err := resolvePath(o, environ)
	if err != nil {
		return nil, err
	}

	loader := dispat.NewLoader(dispat.Options{})
	tree := &dispat.Tree{Root: map[string]any{}}
	if path != "" {
		tree, err = loader.ReadTree(ctx, path)
		if err != nil {
			if !explicit && errors.Is(err, fs.ErrNotExist) {
				tree = &dispat.Tree{Root: map[string]any{}}
			} else {
				return nil, fmt.Errorf("reading config %s: %w", path, err)
			}
		}
	}

	envOverrides, err := EnvBinding(environ).Overrides(ctx)
	if err != nil {
		return nil, err
	}

	settings := tree.Settings(loader, dispat.MergeOverrides(envOverrides, o.FlagOverrides))
	if err := dispat.DecodeObject(settings, "", Fields(&cfg)); err != nil {
		return nil, err
	}

	res := &Result{Config: cfg, Files: tree.Files}
	if path != "" && len(tree.Files) > 0 {
		res.File = path
	}
	return res, nil
}

// resolvePath decides which configuration file to read: the flag, then
// CRIER_CONFIG, then the first default name that exists in Dir.
func resolvePath(o Options, environ []string) (path string, explicit bool, err error) {
	if o.Path != "" {
		return o.Path, true, nil
	}
	if v, ok := lookupEnv(environ, EnvPrefix+"CONFIG"); ok && v != "" {
		return v, true, nil
	}
	dir := o.Dir
	for _, name := range DefaultFileNames {
		candidate := filepath.Join(dir, name)
		if st, statErr := os.Stat(candidate); statErr == nil && !st.IsDir() {
			return candidate, false, nil
		}
	}
	return "", false, nil
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
