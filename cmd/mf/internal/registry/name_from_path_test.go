package registry_test

import (
	"testing"

	"github.com/mguilarducci/mf-cli/cmd/mf/internal/registry"
)

func TestExecutableName(t *testing.T) {
	t.Parallel()
	if got := registry.New(registry.Env{GOOS: "linux"}).ExecutableName("hello"); got != "mf-hello" {
		t.Errorf("ExecutableName on Unix = %q; want mf-hello", got)
	}
	if got := registry.New(registry.Env{GOOS: "windows"}).ExecutableName("hello"); got != "mf-hello.exe" {
		t.Errorf("ExecutableName on Windows = %q; want mf-hello.exe", got)
	}
}

func TestNameFromPath(t *testing.T) {
	t.Parallel()
	unix := registry.New(registry.Env{GOOS: "linux"})
	windows := registry.New(registry.Env{GOOS: "windows", PathExt: "PS1;.EXE"})
	cases := []struct {
		reg  *registry.Registry
		path string
		want string
	}{
		{unix, "./bin/mf-digisac", "digisac"},
		{unix, "/x/mf-my-tool", "my-tool"},
		{unix, "plain", "plain"},
		{unix, "./mf-tool.exe", "tool.exe"},
		{windows, "bin/mf-digisac.EXE", "digisac"},
		{windows, "mf-tool.ps1", "tool"}, // PATHEXT entry without a leading dot
		{windows, "mf-tool.bat", "tool.bat"},
	}
	for _, c := range cases {
		if got := c.reg.NameFromPath(c.path); got != c.want {
			t.Errorf("NameFromPath(%q) = %q; want %q", c.path, got, c.want)
		}
	}
}
