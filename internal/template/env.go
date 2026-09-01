package template

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// EnvPrefixOf reads the prefix out of an `env:PREFIX` data source, and reports
// whether the value was one at all.
//
// A prefix may be empty here — `env:` on its own — because refusing it is
// configuration validation's job, where the error can name the key. This
// function only answers what shape the value is.
func EnvPrefixOf(spec string) (prefix string, ok bool) {
	return strings.CutPrefix(spec, EnvScheme)
}

// CheckEnvPrefix refuses an `env:` source with nothing after it.
//
// An empty prefix would match every variable in the environment — the PATH,
// the shell, whatever CI exports — and hand the lot to the template. That is
// never what somebody meant, and it is the one mistake this source makes easy.
func CheckEnvPrefix(spec string) error {
	prefix, ok := EnvPrefixOf(spec)
	if !ok {
		return nil
	}
	if strings.TrimSpace(prefix) == "" {
		return fmt.Errorf("%q names no prefix; write something like %sCARD_ so it matches "+
			"the variables you meant rather than the whole environment", spec, EnvScheme)
	}
	return nil
}

// DataFromEnv builds a data map from every environment variable carrying the
// prefix.
//
// The mapping is deliberately dull: strip the prefix, lower-case what is left,
// keep the underscores. CARD_TITLE becomes title and CARD_MAIN_TITLE becomes
// main_title. Nothing is split on underscores into nested objects, because a
// flat namespace cannot say whether CARD_MAIN_TITLE is main.title or
// main_title — and guessing would make the answer depend on which other
// variables happen to be set.
//
// Every value is the string as written. No number, no boolean, no date: a
// template overwhelmingly prints what it is given, and a value that silently
// became a float when it looked like one would render 1.0 where "1.0" was
// meant. Structured data belongs in a file, or on stdin.
//
// A nil environ reads the process environment; a non-nil empty slice is an
// empty environment.
func DataFromEnv(prefix string, environ []string) map[string]any {
	if environ == nil {
		environ = os.Environ()
	}
	out := map[string]any{}
	for _, pair := range environ {
		name, value, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		rest, ok := strings.CutPrefix(name, prefix)
		if !ok || rest == "" {
			continue
		}
		// The last occurrence wins, which is how a process environment is read
		// everywhere else.
		out[strings.ToLower(rest)] = value
	}
	return out
}

// EnvNames lists the variables an `env:` source would read, sorted. It is for
// the log line that says what a prefix matched.
func EnvNames(prefix string, environ []string) []string {
	if environ == nil {
		environ = os.Environ()
	}
	var out []string
	for _, pair := range environ {
		name, _, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		if rest, ok := strings.CutPrefix(name, prefix); ok && rest != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
