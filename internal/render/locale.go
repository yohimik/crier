package render

import (
	"os"
	"strings"
)

// NormalizeLocaleEnv rewrites a locale environment that would hand the text
// stack a language tag it cannot survive.
//
// The text layout library derives its default language from LC_ALL, then
// LC_CTYPE, then LANG — the first one set wins — by taking the part before
// the "." and lowercasing it. macOS Terminal sets LC_CTYPE="UTF-8" by
// default, which that rule turns into the language "utf-8", and a tag whose
// second-to-last rune is "-" makes the harfbuzz port index past the end of
// the string: every render on a stock Mac terminal dies. "C" and "POSIX"
// are survivable but just as meaningless as a language, and fontconfig
// complains about them on every run.
//
// So: when the winning variable would produce no real language, LC_ALL is
// set to a locale that does — the first LOSING variable that carries one,
// or en_US.UTF-8 when none does. Setting LC_ALL short-circuits the lookup
// chain, so the result is deterministic; nothing is touched when the
// winning variable is already sane, which keeps the process environment —
// inherited by tunnel and custom-platform subprocesses — as the user wrote
// it in the common case.
func NormalizeLocaleEnv() {
	vars := []string{"LC_ALL", "LC_CTYPE", "LANG"}
	for _, name := range vars {
		v, ok := os.LookupEnv(name)
		if !ok {
			continue
		}
		if localeCarriesLanguage(v) {
			return // the winner is sane; leave everything alone
		}
		// The winner is junk: find a later variable with a real language.
		//
		// A Setenv that fails leaves the junk locale in place, which is the
		// situation this function was already in — so it is worth no more than
		// not pretending it succeeded.
		for _, fallback := range vars {
			if fb, ok := os.LookupEnv(fallback); ok && localeCarriesLanguage(fb) {
				_ = os.Setenv("LC_ALL", fb)
				return
			}
		}
		_ = os.Setenv("LC_ALL", "en_US.UTF-8")
		return
	}
	// No locale variable at all: the library's default language is then
	// empty, which is safe.
}

// localeCarriesLanguage reports whether a locale value like "en_US.UTF-8"
// begins with something usable as a language: letters, before any "." or
// "@", not an encoding name and not the C locale.
func localeCarriesLanguage(locale string) bool {
	lang := locale
	if i := strings.IndexAny(lang, ".@"); i >= 0 {
		lang = lang[:i]
	}
	switch strings.ToLower(lang) {
	case "", "c", "posix", "utf-8", "utf8":
		return false
	}
	for _, r := range lang {
		if !(r == '_' || r == '-' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return false
		}
	}
	return true
}
