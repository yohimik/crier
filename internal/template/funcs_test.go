package template

import (
	"strings"
	"testing"
)

// TestFuncArgumentErrors: every function refuses the wrong number or kind of
// argument with an error that names the problem, rather than panicking or
// guessing. The reflective executor used to do this check for them; now each
// function does it for itself, so each one is asked.
func TestFuncArgumentErrors(t *testing.T) {
	dir := t.TempDir()
	for _, tt := range []struct {
		body string
		want string
	}{
		{`{{ upper }}`, "wrong number of args for upper: want 1 got 0"},
		{`{{ upper 1 }}`, "wrong type for value; expected string; got int"},
		{`{{ lower 1 2 }}`, "wrong number of args for lower"},
		{`{{ trim .list }}`, "expected string"},
		{`{{ join "," 1 }}`, "expected []string"},
		{`{{ join "," .mixed }}`, "item 1 is int"},
		{`{{ join 1 .list }}`, "expected string"},
		{`{{ repeat "a" -1 }}`, "negative count"},
		{`{{ repeat "a" "b" }}`, "expected int"},
		{`{{ repeat 1 1 }}`, "expected string"},
		{`{{ now 1 }}`, "wrong number of args for now"},
		{`{{ date "2006" "not a time" }}`, "expected time.Time"},
		{`{{ date 1 now }}`, "expected string"},
		{`{{ date "2006" }}`, "wrong number of args for date"},
		{`{{ default "x" }}`, "wrong number of args for default"},
		{`{{ randInt 1 }}`, "wrong number of args for randInt"},
		{`{{ randInt "a" 2 }}`, "expected int"},
		{`{{ randInt 1 "b" }}`, "expected int"},
		{`{{ randInt 5 1 }}`, "below the minimum"},
		{`{{ randFloat 1 }}`, "wrong number of args for randFloat"},
		{`{{ randFloat "a" 2 }}`, "expected float64"},
		{`{{ randFloat 1 "b" }}`, "expected float64"},
		{`{{ randFloat 5.0 1.0 }}`, "below the minimum"},
		{`{{ randChoice }}`, "needs at least one item"},
		{`{{ randSeed 1 }}`, "wrong number of args for randSeed"},
	} {
		tpl := write(t, dir, "t.html", tt.body)
		_, err := New().Render(Options{Path: tpl, Extra: map[string]any{
			"list":  []any{"a", "b"},
			"mixed": []any{"a", 1},
		}})
		if err == nil {
			t.Errorf("%s: expected an error", tt.body)
			continue
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want it to mention %q", tt.body, err, tt.want)
		}
	}
}

// TestFuncsAcceptWhatTheyShould: the happy paths the error test's siblings
// have, including the two widenings the executor makes on purpose.
func TestFuncsAcceptWhatTheyShould(t *testing.T) {
	dir := t.TempDir()
	tpl := write(t, dir, "t.html", strings.Join([]string{
		`{{ join "," .list }}`,
		`{{ repeat "-" 3 }}`,
		`{{ randInt 3 3 }}`,
		`{{ randFloat 2 2 }}`,
		`{{ len (randShuffle .list) }}`,
		`{{ len (randShuffle "a" "b" "c") }}`,
		`{{ randChoice .list | len }}`,
		`{{ if randSeed }}seeded{{ end }}`,
		`{{ default "d" .missing }}`,
		`{{ date "2006" now | len }}`,
	}, "|"))
	got, err := NewWithRand(NewRand(7)).Render(Options{Path: tpl, Extra: map[string]any{"list": []any{"a", "b"}}})
	if err != nil {
		t.Fatal(err)
	}
	if want := "a,b|---|3|2|2|3|1|seeded|d|4"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}
