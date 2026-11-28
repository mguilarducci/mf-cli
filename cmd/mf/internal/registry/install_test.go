package registry_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/mguilarducci/mf-cli/cmd/mf/internal/registry"
)

// hostExt is the extension a test script needs to be executable on the host:
// Windows has no executable bit and goes by PATHEXT.
func hostExt() string {
	if runtime.GOOS == "windows" {
		return ".bat"
	}
	return ""
}

// hostRegistry builds a registry for the host OS rooted at a temp dir.
func hostRegistry(t *testing.T) (*registry.Registry, string) {
	t.Helper()
	dir := t.TempDir()
	return registry.New(registry.Env{PluginDir: dir, GOOS: runtime.GOOS}), dir
}

// writeExecutable writes a tiny executable script at path. The ForkLock read
// lock keeps a parallel fork from inheriting the open file, which would make
// exec fail with ETXTBSY (golang/go#22315), as entireio/cli's
// writeExecutableScript does.
func writeExecutable(t *testing.T, path string) string {
	t.Helper()
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n# "+path+"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInstallLocalSymlinks(t *testing.T) {
	t.Parallel()
	r, dir := hostRegistry(t)
	src := writeExecutable(t, filepath.Join(t.TempDir(), "mf-hello"+hostExt()))

	dst, err := r.InstallLocal(src, "hello", false)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "bin", "mf-hello"+hostExt()); dst != want {
		t.Errorf("InstallLocal() = %q; want %q", dst, want)
	}
	if runtime.GOOS == "windows" {
		return // symlinks need extra privileges there; install falls back to a copy
	}
	if target, err := os.Readlink(dst); err != nil || target != src {
		t.Errorf("Readlink(%q) = %q, %v; want %q", dst, target, err, src)
	}
}

func TestInstallRefusesExistingWithoutForce(t *testing.T) {
	t.Parallel()
	r, _ := hostRegistry(t)
	first := writeExecutable(t, filepath.Join(t.TempDir(), "mf-hello"+hostExt()))
	dst, err := r.InstallLocal(first, "hello", false)
	if err != nil {
		t.Fatal(err)
	}
	second := writeExecutable(t, filepath.Join(t.TempDir(), "mf-hello"+hostExt()))

	_, err = r.InstallLocal(second, "hello", false)
	if err == nil || !strings.Contains(err.Error(), dst) {
		t.Fatalf("InstallLocal without force err = %v; want refusal naming %s", err, dst)
	}
	assertEntryPointsAt(t, dst, first)
}

func TestCanInstallChangesNothing(t *testing.T) {
	t.Parallel()
	r, dir := hostRegistry(t)

	if err := r.CanInstall("hello", false); err != nil {
		t.Errorf("CanInstall on empty registry = %v; want nil", err)
	}
	if _, err := os.Stat(dir + "/bin"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("CanInstall created the bin dir (stat err %v)", err)
	}

	src := writeExecutable(t, filepath.Join(t.TempDir(), "mf-hello"+hostExt()))
	dst, err := r.InstallLocal(src, "hello", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CanInstall("hello", false); err == nil || !strings.Contains(err.Error(), dst) {
		t.Errorf("CanInstall without force = %v; want refusal naming %s", err, dst)
	}
	if err := r.CanInstall("hello", true); err != nil {
		t.Errorf("CanInstall with force = %v; want nil", err)
	}
	assertEntryPointsAt(t, dst, src)
	if err := r.CanInstall("Bad/Name", true); err == nil {
		t.Error("CanInstall(invalid name) = nil; want error")
	}
}

// assertEntryPointsAt checks that the registry entry dst still runs src:
// a symlink to it, or (when symlinks are refused) a copy of it.
func assertEntryPointsAt(t *testing.T, dst, src string) {
	t.Helper()
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read entry %s: %v", dst, err)
	}
	want, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("entry %s has content %q; want that of %s", dst, got, src)
	}
	if target, err := os.Readlink(dst); err == nil && target != src {
		t.Errorf("entry %s links to %q; want %q", dst, target, src)
	}
}

func TestInstallForceReplaces(t *testing.T) {
	t.Parallel()
	r, dir := hostRegistry(t)
	first := writeExecutable(t, filepath.Join(t.TempDir(), "mf-hello"+hostExt()))
	if _, err := r.InstallLocal(first, "hello", false); err != nil {
		t.Fatal(err)
	}
	// A stale package from an earlier repository install.
	pkg := filepath.Join(dir, "pkg", "hello")
	if err := os.MkdirAll(pkg, 0o750); err != nil {
		t.Fatal(err)
	}
	second := writeExecutable(t, filepath.Join(t.TempDir(), "mf-hello"+hostExt()))

	dst, err := r.InstallLocal(second, "hello", true)
	if err != nil {
		t.Fatalf("InstallLocal with force: %v", err)
	}
	assertEntryPointsAt(t, dst, second)
	if _, err := os.Stat(pkg); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("old package %s survived a forced install (stat err %v)", pkg, err)
	}
	assertBinDir(t, dir, "mf-hello"+hostExt())
}

func TestInstallRefusesFileOntoItself(t *testing.T) {
	t.Parallel()
	r, dir := hostRegistry(t)
	src := writeExecutable(t, filepath.Join(t.TempDir(), "mf-hello"+hostExt()))
	if _, err := r.InstallLocal(src, "hello", false); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "bin", "mf-hello"+hostExt())

	if _, err := r.InstallLocal(filepath.Join(dir, "bin", ".", "mf-hello"+hostExt()), "hello", true); err == nil {
		t.Fatal("installing the registry entry onto itself: want error")
	}
	assertEntryPointsAt(t, dst, src)
}

func TestInstallRejectsBadSources(t *testing.T) {
	t.Parallel()
	r, dir := hostRegistry(t)
	src := writeExecutable(t, filepath.Join(t.TempDir(), "mf-hello"+hostExt()))
	for _, name := range []string{"", "-flag", "Upper", "../escape", "a/b"} {
		if _, err := r.InstallLocal(src, name, false); err == nil {
			t.Errorf("InstallLocal(name %q) = nil error; want rejection", name)
		}
	}
	data := filepath.Join(t.TempDir(), "mf-data")
	if err := os.WriteFile(data, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.InstallLocal(data, "data", false); err == nil {
		t.Error("InstallLocal(non-executable) = nil error; want rejection")
	}
	if _, err := r.InstallLocal(t.TempDir(), "dir", false); err == nil {
		t.Error("InstallLocal(directory) = nil error; want rejection")
	}
	if _, err := r.InstallLocal(filepath.Join(t.TempDir(), "missing"), "missing", false); err == nil {
		t.Error("InstallLocal(missing file) = nil error; want rejection")
	}
	assertBinDir(t, dir)
}

// assertBinDir checks the registry's bin dir holds exactly want: no other
// variant and no leftover temporary file.
func assertBinDir(t *testing.T, dir string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "bin"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if !slices.Equal(got, want) {
		t.Errorf("bin dir = %v; want %v", got, want)
	}
}

func TestInstallForceRefusesSourceInsideReplacedPackage(t *testing.T) {
	t.Parallel()
	r, dir := hostRegistry(t)
	pkg := filepath.Join(dir, "pkg", "hello")
	if err := os.MkdirAll(pkg, 0o750); err != nil {
		t.Fatal(err)
	}
	pkgBin := writeExecutable(t, filepath.Join(pkg, "mf-hello"+hostExt()))
	dst, err := r.InstallLocal(pkgBin, "hello", false) // how a repository install links its package
	if err != nil {
		t.Fatal(err)
	}

	if _, err := r.InstallLocal(pkgBin, "hello", true); err == nil {
		t.Fatal("forced install from the package it replaces: want error")
	}
	assertEntryPointsAt(t, dst, pkgBin)
}
