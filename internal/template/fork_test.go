package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryExecutionSeesTheSameChoices is the regression test.
//
// The random helpers were bound once to a stream shared by the whole run, and
// a stream advances as it is read. So the second execution of a template
// continued where the first left off and chose differently — which means every
// frame of a video picked a new accent colour (a strobe), and each platform
// variant differed from the card it is a variant of. The type's own
// documentation promises the opposite.
func TestEveryExecutionSeesTheSameChoices(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.html")
	if err := os.WriteFile(path, []byte(
		`{{ randChoice "a" "b" "c" "d" "e" "f" "g" "h" }}|{{ randInt 1 1000 }}|{{ printf "%.4f" (randFloat 0.0 1.0) }}`,
	), 0o600); err != nil {
		t.Fatal(err)
	}

	e := NewWithRand(NewRand(20260901))

	first, err := e.RenderWith(Options{Path: path}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := e.RenderWith(Options{Path: path}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if again != first {
			t.Fatalf("execution %d chose %q, the first chose %q", i+2, again, first)
		}
	}

	// A variant is another execution with different overlays: the choices are
	// still the run's.
	overlay := filepath.Join(dir, "o.html")
	if err := os.WriteFile(overlay, []byte(`{{ define "unused" }}x{{ end }}`), 0o600); err != nil {
		t.Fatal(err)
	}
	variant, err := e.RenderWith(Options{Path: path, Overlays: []string{overlay}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if variant != first {
		t.Errorf("a variant chose %q, the base chose %q", variant, first)
	}

	// A caption is an execution too, and starts from the same place.
	caption, err := e.RenderCaption(`{{ randChoice "a" "b" "c" "d" "e" "f" "g" "h" }}`, nil, "telegram")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.SplitN(first, "|", 2)[0]; caption != want {
		t.Errorf("the caption chose %q and the layout chose %q", caption, want)
	}
}

// TestADifferentSeedChoosesDifferently: forking must not have flattened the
// randomness into a constant.
func TestADifferentSeedChoosesDifferently(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.html")
	if err := os.WriteFile(path, []byte(`{{ randInt 1 1000000 }}`), 0o600); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, seed := range []int64{1, 2, 3, 20260901, 777} {
		out, err := NewWithRand(NewRand(seed)).RenderWith(Options{Path: path}, nil)
		if err != nil {
			t.Fatal(err)
		}
		seen[out] = true
	}
	if len(seen) < 4 {
		t.Errorf("five seeds produced %d distinct values: %v", len(seen), seen)
	}
}

// TestForkLeavesTheRunStreamAlone: the pool pick draws from the run's own
// stream, and forking for a template must not rewind or disturb it.
func TestForkLeavesTheRunStreamAlone(t *testing.T) {
	r := NewRand(42)
	first := r.IntN(1 << 30)

	fork := r.Fork()
	_ = fork.IntN(1 << 30)
	_ = fork.IntN(1 << 30)

	second := r.IntN(1 << 30)
	if first == second {
		t.Error("the run stream did not advance")
	}

	// Two forks of the same source are identical to each other.
	a, b := r.Fork(), r.Fork()
	for i := 0; i < 8; i++ {
		if x, y := a.IntN(1000), b.IntN(1000); x != y {
			t.Fatalf("fork %d gave %d and %d", i, x, y)
		}
	}
	if a.Seed() != r.Seed() {
		t.Errorf("a fork reports seed %d, the run is %d", a.Seed(), r.Seed())
	}
}
