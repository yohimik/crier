package version

import (
	"strings"
	"testing"
)

func TestGetPopulatesRuntimeFields(t *testing.T) {
	got := Get()
	if got.GoVersion == "" {
		t.Fatal("GoVersion must not be empty")
	}
	if !strings.Contains(got.Platform, "/") {
		t.Fatalf("Platform %q should look like os/arch", got.Platform)
	}
}

func TestInfoString(t *testing.T) {
	i := Info{Version: "v1.0.0", Commit: "abc", Date: "2026-01-01", GoVersion: "go1.25", Platform: "linux/amd64"}
	s := i.String()
	for _, want := range []string{"v1.0.0", "abc", "2026-01-01", "go1.25", "linux/amd64"} {
		if !strings.Contains(s, want) {
			t.Errorf("String() = %q, missing %q", s, want)
		}
	}
}
