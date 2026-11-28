package registry_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mguilarducci/mf-cli/cmd/mf/internal/registry"
)

// Windows rules come from the snapshot, so they run on every OS. Windows
// needs no executable bit: the extension decides.
func windowsRegistry(t *testing.T, pathExt string) (*registry.Registry, string) {
	t.Helper()
	dir := t.TempDir()
	return registry.New(registry.Env{PluginDir: dir, GOOS: "windows", PathExt: pathExt}), dir
}

func writePlain(t *testing.T, path string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte("@echo off\r\n"+path+"\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWindowsInstallKeepsExtension(t *testing.T) {
	t.Parallel()
	r, dir := windowsRegistry(t, "")
	src := writePlain(t, filepath.Join(t.TempDir(), "mf-hello.BAT"))

	dst, err := r.InstallLocal(src, "hello", false)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "bin", "mf-hello.bat"); dst != want {
		t.Errorf("InstallLocal() = %q; want %q", dst, want)
	}
	if _, err := r.InstallLocal(writePlain(t, filepath.Join(t.TempDir(), "mf-hello")), "noext", false); err == nil {
		t.Error("InstallLocal(no extension) on Windows = nil error; want rejection")
	}
}

func TestWindowsCustomPathExt(t *testing.T) {
	t.Parallel()
	r, dir := windowsRegistry(t, ".PS1;.EXE")
	if _, err := r.InstallLocal(writePlain(t, filepath.Join(t.TempDir(), "mf-hello.ps1")), "hello", false); err != nil {
		t.Fatalf("InstallLocal(.ps1 listed in PATHEXT): %v", err)
	}
	if _, err := r.InstallLocal(writePlain(t, filepath.Join(t.TempDir(), "mf-other.bat")), "other", false); err == nil {
		t.Error("InstallLocal(.bat not in PATHEXT) = nil error; want rejection")
	}
	assertBinDir(t, dir, "mf-hello.ps1")
}

func TestWindowsForceReplacesEveryVariant(t *testing.T) {
	t.Parallel()
	r, dir := windowsRegistry(t, "")
	if _, err := r.InstallLocal(writePlain(t, filepath.Join(t.TempDir(), "mf-hello.bat")), "hello", false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.InstallLocal(writePlain(t, filepath.Join(t.TempDir(), "mf-hello.cmd")), "hello", false); err == nil {
		t.Fatal("installing another extension variant without force: want refusal")
	}

	exe := writePlain(t, filepath.Join(t.TempDir(), "mf-hello.exe"))
	dst, err := r.InstallLocal(exe, "hello", true)
	if err != nil {
		t.Fatal(err)
	}
	assertEntryPointsAt(t, dst, exe)
	assertBinDir(t, dir, "mf-hello.exe")
}

func TestWindowsRemoveDeletesEveryVariant(t *testing.T) {
	t.Parallel()
	r, dir := windowsRegistry(t, "")
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"mf-hello.exe", "mf-hello.cmd", "mf-other.exe"} {
		writePlain(t, filepath.Join(bin, n))
	}

	if err := r.Remove("hello", false); err != nil {
		t.Fatal(err)
	}
	assertBinDir(t, dir, "mf-other.exe")
}
