package render

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clearLocale unsets every locale variable so each case starts from nothing.
func clearLocale(t *testing.T) {
	t.Helper()
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		t.Setenv(name, "")
		require.NoError(t, os.Unsetenv(name))
	}
}

func TestNormalizeLocaleEnvRewritesTheMacTerminalDefault(t *testing.T) {
	clearLocale(t)
	t.Setenv("LC_CTYPE", "UTF-8")

	NormalizeLocaleEnv()

	assert.Equal(t, "en_US.UTF-8", os.Getenv("LC_ALL"))
}

func TestNormalizeLocaleEnvPrefersTheUsersRealLocale(t *testing.T) {
	clearLocale(t)
	t.Setenv("LC_CTYPE", "UTF-8")
	t.Setenv("LANG", "ru_RU.UTF-8")

	NormalizeLocaleEnv()

	assert.Equal(t, "ru_RU.UTF-8", os.Getenv("LC_ALL"), "the losing variable that carries a language wins")
}

func TestNormalizeLocaleEnvLeavesASaneEnvironmentAlone(t *testing.T) {
	clearLocale(t)
	t.Setenv("LANG", "de_DE.UTF-8")

	NormalizeLocaleEnv()

	_, set := os.LookupEnv("LC_ALL")
	assert.False(t, set, "nothing to fix, nothing touched")
	assert.Equal(t, "de_DE.UTF-8", os.Getenv("LANG"))
}

func TestNormalizeLocaleEnvLeavesAnEmptyEnvironmentAlone(t *testing.T) {
	clearLocale(t)

	NormalizeLocaleEnv()

	_, set := os.LookupEnv("LC_ALL")
	assert.False(t, set, "no locale at all is already safe")
}

func TestNormalizeLocaleEnvTreatsTheCLocaleAsNoLanguage(t *testing.T) {
	clearLocale(t)
	t.Setenv("LC_ALL", "C.UTF-8")

	NormalizeLocaleEnv()

	assert.Equal(t, "en_US.UTF-8", os.Getenv("LC_ALL"))
}

func TestLocaleCarriesLanguage(t *testing.T) {
	for locale, want := range map[string]bool{
		"en_US.UTF-8": true,
		"de_DE":       true,
		"pt-BR.UTF-8": true,
		"UTF-8":       false,
		"utf8":        false,
		"C":           false,
		"C.UTF-8":     false,
		"POSIX":       false,
		"":            false,
		"123.UTF-8":   false,
	} {
		assert.Equal(t, want, localeCarriesLanguage(locale), "locale %q", locale)
	}
}

// TestRenderSurvivesTheMacTerminalLocale: LC_CTYPE=UTF-8 — a stock macOS
// terminal — made the harfbuzz port index past the end of the "utf-8"
// language tag and killed the process. The normalize step runs first in the
// real binary; this asserts the combination end to end.
func TestRenderSurvivesTheMacTerminalLocale(t *testing.T) {
	clearLocale(t)
	t.Setenv("LC_CTYPE", "UTF-8")
	NormalizeLocaleEnv()

	fonts, err := NewFonts(FontOptions{Hermetic: true})
	require.NoError(t, err)
	imgs, err := Render(context.Background(), Options{
		HTML:   `<html><body style="font-family: Go">locale test</body></html>`,
		Width:  200,
		Height: 100,
		Fonts:  fonts,
	})
	require.NoError(t, err)
	require.Len(t, imgs, 1)
}

// TestRenderTurnsALayoutPanicIntoAnError: a lang attribute shaped like the
// broken tag still reaches the layout engine without going through the
// locale, and the engine still panics on it. The render must fail with an
// error that says so, not take the process down.
func TestRenderTurnsALayoutPanicIntoAnError(t *testing.T) {
	fonts, err := NewFonts(FontOptions{Hermetic: true})
	require.NoError(t, err)
	_, err = Render(context.Background(), Options{
		HTML:   `<html lang="utf-8"><body style="font-family: Go">hostile tag</body></html>`,
		Width:  200,
		Height: 100,
		Fonts:  fonts,
	})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "layout engine crashed"), "got: %v", err)
}
