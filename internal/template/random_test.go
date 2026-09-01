package template

import (
	"strings"
	"testing"
)

func TestSameSeedGivesTheSameOutput(t *testing.T) {
	dir := t.TempDir()
	tpl := write(t, dir, "t.html", `{{ randChoice "a" "b" "c" }}-{{ randInt 1 100 }}-{{ printf "%.4f" (randFloat 0 1) }}`)

	first, err := NewWithRand(NewRand(42)).Render(Options{Path: tpl})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewWithRand(NewRand(42)).Render(Options{Path: tpl})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("the same seed gave %q then %q", first, second)
	}

	other, err := NewWithRand(NewRand(43)).Render(Options{Path: tpl})
	if err != nil {
		t.Fatal(err)
	}
	if other == first {
		t.Errorf("two seeds gave the same output %q; the seed is not reaching the functions", first)
	}
}

func TestRandSeedIsReportedAndReproducible(t *testing.T) {
	r := NewRand(1234)
	if r.Seed() != 1234 {
		t.Errorf("seed = %d", r.Seed())
	}
	auto := NewRand(0)
	if auto.Seed() == 0 {
		t.Error("a drawn seed must not be zero, or it would mean 'draw another'")
	}
	// The drawn seed reproduces the run.
	a := NewWithRand(NewRand(auto.Seed()))
	b := NewWithRand(NewRand(auto.Seed()))
	if a.Rand().IntN(1000) != b.Rand().IntN(1000) {
		t.Error("the reported seed does not reproduce the sequence")
	}
}

func TestRandFuncsTakeAListOrArguments(t *testing.T) {
	dir := t.TempDir()
	tpl := write(t, dir, "t.html", `{{ randChoice .colours }}|{{ randChoice "x" }}`)
	got, err := NewWithRand(NewRand(7)).Render(Options{
		Path:  tpl,
		Extra: map[string]any{"colours": []any{"#f00", "#0f0", "#00f"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(got, "|")
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "#") || parts[1] != "x" {
		t.Errorf("got %q", got)
	}

	tpl = write(t, dir, "s.html", `{{ range randShuffle .items }}{{ . }}{{ end }}`)
	got, err = NewWithRand(NewRand(7)).Render(Options{
		Path:  tpl,
		Extra: map[string]any{"items": []string{"a", "b", "c", "d"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Errorf("a shuffle should keep every item: %q", got)
	}
	for _, want := range []string{"a", "b", "c", "d"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is missing from %q", want, got)
		}
	}
}

func TestRandFuncErrors(t *testing.T) {
	dir := t.TempDir()
	for _, body := range []string{
		`{{ randChoice }}`,
		`{{ randInt 10 1 }}`,
		`{{ randFloat 10 1 }}`,
	} {
		tpl := write(t, dir, "t.html", body)
		if _, err := NewWithRand(NewRand(1)).Render(Options{Path: tpl}); err == nil {
			t.Errorf("%s should have failed", body)
		}
	}
}

func TestRandIntBounds(t *testing.T) {
	dir := t.TempDir()
	tpl := write(t, dir, "t.html", `{{ randInt 5 5 }}`)
	got, err := NewWithRand(NewRand(1)).Render(Options{Path: tpl})
	if err != nil {
		t.Fatal(err)
	}
	if got != "5" {
		t.Errorf("a single-value range gives that value, got %q", got)
	}

	r := NewRand(9)
	for i := 0; i < 200; i++ {
		if n := r.IntN(3); n < 0 || n > 2 {
			t.Fatalf("IntN(3) = %d", n)
		}
		if f := r.Float64(); f < 0 || f >= 1 {
			t.Fatalf("Float64 = %v", f)
		}
	}
	if r.IntN(0) != 0 {
		t.Error("IntN(0) should be harmless")
	}
	if _, ok := r.Choose(0); ok {
		t.Error("choosing from nothing should say so")
	}
}

func TestCaptionsSeeTheRandomFuncs(t *testing.T) {
	e := NewWithRand(NewRand(11))
	got, err := e.RenderCaption(`{{ randChoice "one" "two" }}`, nil, "telegram")
	if err != nil {
		t.Fatal(err)
	}
	if got != "one" && got != "two" {
		t.Errorf("got %q", got)
	}
	again, err := NewWithRand(NewRand(11)).RenderCaption(`{{ randChoice "one" "two" }}`, nil, "telegram")
	if err != nil {
		t.Fatal(err)
	}
	if again != got {
		t.Errorf("the same seed gave %q then %q", got, again)
	}
}

func TestPickChoosesFromThePool(t *testing.T) {
	e := NewWithRand(NewRand(3))
	if _, ok := e.Pick(nil); ok {
		t.Error("an empty pool picks nothing")
	}
	if _, ok := e.Pick([]string{"  ", ""}); ok {
		t.Error("a pool of blanks picks nothing")
	}
	got, ok := e.Pick([]string{"a.html"})
	if !ok || got != "a.html" {
		t.Errorf("got %q %v", got, ok)
	}

	// Over many seeds, both entries of a pair come up.
	seen := map[string]bool{}
	for seed := int64(1); seed <= 40; seed++ {
		pick, _ := NewWithRand(NewRand(seed)).Pick([]string{"a.html", "b.html"})
		seen[pick] = true
	}
	if len(seen) != 2 {
		t.Errorf("a pool of two only ever produced %v", seen)
	}

	// And one seed always produces the same pick.
	first, _ := NewWithRand(NewRand(5)).Pick([]string{"a.html", "b.html"})
	second, _ := NewWithRand(NewRand(5)).Pick([]string{"a.html", "b.html"})
	if first != second {
		t.Errorf("one seed gave %q then %q", first, second)
	}
}

func TestFlatten(t *testing.T) {
	if got := flatten([]any{[]string{"a", "b"}}); len(got) != 2 {
		t.Errorf("a []string should flatten: %v", got)
	}
	if got := flatten([]any{[]any{1, 2, 3}}); len(got) != 3 {
		t.Errorf("a []any should flatten: %v", got)
	}
	if got := flatten([]any{"a", "b"}); len(got) != 2 {
		t.Errorf("arguments stay as they are: %v", got)
	}
	if got := flatten([]any{"only"}); len(got) != 1 {
		t.Errorf("a single scalar is one item: %v", got)
	}
}

func TestNewWithNilRandStillWorks(t *testing.T) {
	e := NewWithRand(nil)
	if e.Rand() == nil || e.Rand().Seed() == 0 {
		t.Error("a nil source should be replaced by a real one")
	}
}
