package registry_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mguilarducci/mf-cli/cmd/mf/internal/registry"
)

// writePlugin creates an executable file mf-<name> in dir (mf-<name>.exe on
// Windows hosts, which match by extension) and returns its path. Nothing
// here is ever run.
func writePlugin(t *testing.T, dir, name string) string {
	t.Helper()
	file := "mf-" + name
	if runtime.GOOS == "windows" {
		file += ".exe"
	}
	return writeFile(t, dir, file, 0o755)
}

func writeFile(t *testing.T, dir, file string, mode os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, file)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestManagedPluginBeatsPath(t *testing.T) {
	t.Parallel()
	regDir, pathDir := t.TempDir(), t.TempDir()
	managed := writePlugin(t, filepath.Join(regDir, "bin"), "hello")
	writePlugin(t, pathDir, "hello")
	r := registry.New(registry.Env{PluginDir: regDir, Path: []string{pathDir}, GOOS: runtime.GOOS})

	p, ok := r.Resolve("hello")
	if !ok || p.Path != managed || !p.Managed || p.Name != "hello" {
		t.Errorf("Resolve(hello) = %+v, %v; want managed %s", p, ok, managed)
	}
}

func TestFirstMatchWinsAcrossPath(t *testing.T) {
	t.Parallel()
	first, second := t.TempDir(), t.TempDir()
	want := writePlugin(t, first, "hello")
	writePlugin(t, second, "hello")
	r := registry.New(registry.Env{Path: []string{first, second}, GOOS: runtime.GOOS})

	p, ok := r.Resolve("hello")
	if !ok || p.Path != want || p.Managed {
		t.Errorf("Resolve(hello) = %+v, %v; want unmanaged %s", p, ok, want)
	}
}

// relativeTo returns dir as a path relative to the working directory, such
// as ../../../tmp/x/bin: a PATH entry that contains a separator, which Go's
// ErrDot check alone would not catch.
func relativeTo(t *testing.T, dir string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(wd, dir)
	if err != nil {
		t.Skipf("no relative path from %s to %s: %v", wd, dir, err)
	}
	return rel
}

func TestRelativeAndEmptyPathEntriesSkipped(t *testing.T) {
	t.Parallel()
	planted := filepath.Join(t.TempDir(), "bin")
	writePlugin(t, planted, "hello")
	r := registry.New(registry.Env{Path: []string{"", ".", relativeTo(t, planted)}, GOOS: runtime.GOOS})

	if p, ok := r.Resolve("hello"); ok {
		t.Errorf("Resolve(hello) = %+v; want nothing through relative PATH entries", p)
	}
}

func TestInvalidNamesNeverResolve(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writePlugin(t, dir, "Upper")
	writePlugin(t, dir, "-flag")
	r := registry.New(registry.Env{Path: []string{dir}, GOOS: runtime.GOOS})
	for _, name := range []string{"", "Upper", "-flag", "../x", "a/b", ".."} {
		if p, ok := r.Resolve(name); ok {
			t.Errorf("Resolve(%q) = %+v; want nothing", name, p)
		}
	}
}

func TestUnixNeedsExecutableRegularFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "mf-data", 0o644)
	if err := os.Mkdir(filepath.Join(dir, "mf-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := registry.New(registry.Env{Path: []string{dir}, GOOS: "linux"})
	for _, name := range []string{"data", "dir"} {
		if p, ok := r.Resolve(name); ok {
			t.Errorf("Resolve(%q) = %+v; want nothing", name, p)
		}
	}
}

func TestWindowsResolvesByPathExt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		pathExt string
		files   []string
		want    string // file that resolves; empty for none
	}{
		{"empty PATHEXT uses default order", "", []string{"mf-hello.bat", "mf-hello.exe"}, "mf-hello.exe"},
		{"custom PATHEXT order wins over listing", ".BAT;.EXE", []string{"mf-hello.exe", "mf-hello.bat"}, "mf-hello.bat"},
		{"missing leading dot is added", "ps1", []string{"mf-hello.ps1"}, "mf-hello.ps1"},
		{"extension outside PATHEXT", ".exe", []string{"mf-hello.bat"}, ""},
		{"no extension never matches", "", []string{"mf-hello"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for _, f := range c.files {
				writeFile(t, dir, f, 0o644) // Windows has no executable bit
			}
			r := registry.New(registry.Env{Path: []string{dir}, PathExt: c.pathExt, GOOS: "windows"})
			p, ok := r.Resolve("hello")
			switch {
			case c.want == "" && ok:
				t.Errorf("Resolve(hello) = %+v; want nothing", p)
			case c.want != "" && (!ok || p.Path != filepath.Join(dir, c.want)):
				t.Errorf("Resolve(hello) = %+v, %v; want %s", p, ok, c.want)
			}
		})
	}
}
