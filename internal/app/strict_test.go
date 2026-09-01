package app

import (
	"strings"
	"testing"
)

// TestUnknownCommandListsTheValidOnes: refusing is only half the job — a
// refusal that does not say what was expected makes the user go and read the
// source.
func TestUnknownCommandListsTheValidOnes(t *testing.T) {
	code, _, stderr := run(t, t.TempDir(), []string{}, "pubish")
	if code != ExitUsage {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(stderr, `unknown command "pubish"`) {
		t.Errorf("the refusal should name the word: %q", stderr)
	}
	for _, name := range Commands {
		if !strings.Contains(stderr, name) {
			t.Errorf("%s is missing from the list of valid commands:\n%s", name, stderr)
		}
	}
}

// TestMistypedFlagIsRefusedRatherThanRouted is the rule that matters most: a
// leading flag reaches publish, and publish must refuse one it never declared
// rather than publish for real while the operator thinks they asked for a dry
// run.
func TestMistypedFlagIsRefusedRatherThanRouted(t *testing.T) {
	for _, arg := range []string{"--piblish", "--dry-runn", "--render-widht"} {
		code, stdout, stderr := run(t, t.TempDir(), []string{}, arg)
		if code != ExitUsage {
			t.Errorf("%s: code = %d, want a usage error", arg, code)
		}
		if !strings.Contains(stderr, strings.TrimPrefix(arg, "--")) {
			t.Errorf("%s: the error should name the flag: %q", arg, stderr)
		}
		if stdout != "" {
			t.Errorf("%s: something ran anyway: %q", arg, stdout)
		}
	}
}

func TestUnknownSubcommandFlagIsAUsageError(t *testing.T) {
	for _, args := range [][]string{
		{"render", "--nope"},
		{"config", "--nope"},
		{"platforms", "--nope"},
		{"ping", "--nope"},
		{"init", "--nope"},
		{"self-update", "--nope"},
		{"--version", "--nope"},
	} {
		code, _, stderr := run(t, t.TempDir(), []string{}, args...)
		if code != ExitUsage {
			t.Errorf("%v: code = %d, stderr = %q", args, code, stderr)
		}
	}
}

// TestPositionalArgumentsAreRefused: crier takes no positional arguments
// anywhere, so a stray word is a mistake — most often a flag whose value drifted.
func TestPositionalArgumentsAreRefused(t *testing.T) {
	for _, args := range [][]string{
		{"render", "template.html"},
		{"config", "extra"},
		{"init", "somewhere"},
		{"--version", "extra"},
		{"--dry-run", "stray"},
	} {
		code, _, stderr := run(t, t.TempDir(), []string{}, args...)
		if code != ExitUsage {
			t.Errorf("%v: code = %d, stderr = %q", args, code, stderr)
		}
		if !strings.Contains(stderr, "unexpected argument") {
			t.Errorf("%v: stderr = %q", args, stderr)
		}
	}
}

// TestUnknownSetKeyIsAConfigError: --set is the escape hatch for keys that
// have no flag, so it is also the one place a typo could quietly go nowhere.
func TestUnknownSetKeyIsAConfigError(t *testing.T) {
	dir := project(t, "")
	code, _, stderr := run(t, dir, []string{}, "config", "--set", "render.widht=900")
	if code != ExitConfig {
		t.Fatalf("code = %d, want a config error", code)
	}
	if !strings.Contains(stderr, "unknown key") {
		t.Errorf("stderr = %q", stderr)
	}
	// And it points at the section it does know, rather than only saying no.
	if !strings.Contains(stderr, "render.") {
		t.Errorf("no suggestion: %q", stderr)
	}

	if code, _, stderr := run(t, dir, []string{}, "config", "--set", "nonsense"); code != ExitConfig {
		t.Errorf("a --set with no = : code = %d, stderr = %q", code, stderr)
	}
}

func TestSetOverridesAnyKey(t *testing.T) {
	dir := project(t, "")
	code, stdout, stderr := run(t, dir, []string{}, "config", "--set", "render.width=777", "--json")
	if code != ExitOK {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "777") {
		t.Errorf("--set did not reach the configuration:\n%s", stdout)
	}

	// Repeatable, and it outranks the key's own flag: --set is the most
	// specific thing that can have been typed.
	code, stdout, _ = run(t, dir, []string{}, "config", "--json",
		"--render-width", "111", "--set", "render.width=222", "--set", "render.height=333")
	if code != ExitOK || !strings.Contains(stdout, "222") || !strings.Contains(stdout, "333") {
		t.Errorf("code=%d stdout=%s", code, stdout)
	}
}

// A signature rather than a feature. It is tested so it cannot break silently,
// and deliberately absent from the usage text and the docs.
func TestSemenIsSleeping(t *testing.T) {
	code, stdout, _ := run(t, t.TempDir(), []string{}, "semen")
	if code != ExitOK || stdout != "semen is sleeping\n" {
		t.Errorf("code=%d stdout=%q", code, stdout)
	}
	if strings.Contains(strings.Join(Commands, " "), "semen") {
		t.Error("it should not be advertised in the command list")
	}
}
