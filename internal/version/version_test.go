package version

import (
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	v := Version()
	if v == "" {
		t.Error("Version() returned empty string")
	}
	// Default value should be "dev" when built without ldflags
	if v != "dev" {
		t.Errorf("Version() = %q, want %q (default)", v, "dev")
	}
}

func TestCommit(t *testing.T) {
	c := Commit()
	if c == "" {
		t.Error("Commit() returned empty string")
	}
	if c != "none" {
		t.Errorf("Commit() = %q, want %q (default)", c, "none")
	}
}

func TestDate(t *testing.T) {
	d := Date()
	if d == "" {
		t.Error("Date() returned empty string")
	}
	if d != "unknown" {
		t.Errorf("Date() = %q, want %q (default)", d, "unknown")
	}
}

func TestBuiltBy(t *testing.T) {
	b := BuiltBy()
	if b == "" {
		t.Error("BuiltBy() returned empty string")
	}
	if b != "unknown" {
		t.Errorf("BuiltBy() = %q, want %q (default)", b, "unknown")
	}
}

func TestString(t *testing.T) {
	s := String()
	if s == "" {
		t.Error("String() returned empty string")
	}
	// Should contain "tributary" and the version
	if !strings.Contains(s, "tributary") {
		t.Errorf("String() = %q, should contain 'tributary'", s)
	}
	if !strings.Contains(s, "dev") {
		t.Errorf("String() = %q, should contain version 'dev'", s)
	}
	if !strings.Contains(s, "commit:") {
		t.Errorf("String() = %q, should contain 'commit:'", s)
	}
	if !strings.Contains(s, "built:") {
		t.Errorf("String() = %q, should contain 'built:'", s)
	}
}

func TestShort(t *testing.T) {
	s := Short()
	if s == "" {
		t.Error("Short() returned empty string")
	}
	if s != "dev" {
		t.Errorf("Short() = %q, want %q (default)", s, "dev")
	}
}

func TestGoVersion(t *testing.T) {
	gv := GoVersion()
	if gv == "" {
		t.Error("GoVersion() returned empty string")
	}
	// Should start with "go"
	if !strings.HasPrefix(gv, "go") {
		t.Errorf("GoVersion() = %q, should start with 'go'", gv)
	}
}

func TestPlatform(t *testing.T) {
	p := Platform()
	if p == "" {
		t.Error("Platform() returned empty string")
	}
	// Should contain "/" separator between OS and arch
	if !strings.Contains(p, "/") {
		t.Errorf("Platform() = %q, should contain '/'", p)
	}
	// Should be either darwin, linux, or windows
	parts := strings.Split(p, "/")
	if len(parts) != 2 {
		t.Errorf("Platform() = %q, should have format 'os/arch'", p)
	}
}
