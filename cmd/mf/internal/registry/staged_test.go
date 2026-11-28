package registry_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mguilarducci/mf-cli/cmd/mf/internal/registry"
)

// stage writes an executable where a repository install leaves its
// downloaded binary, outside the registry.
func stage(t *testing.T) string {
	t.Helper()
	return writeExecutable(t, filepath.Join(t.TempDir(), "extracted-mf-hello"+hostExt()))
}

func manifest(tag string) *registry.Manifest {
	return &registry.Manifest{
		Name:        "hello",
		RepoURL:     "https://example.com/mf-hello",
		Tag:         tag,
		InstalledAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

func TestInstallStagedPlacesBinaryAndManifest(t *testing.T) {
	t.Parallel()
	r, dir := hostRegistry(t)
	staged := stage(t)
	content, err := os.ReadFile(staged)
	if err != nil {
		t.Fatal(err)
	}

	dst, err := r.InstallStaged(staged, manifest("v1.0.0"), false)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "bin", "mf-hello"+hostExt()); dst != want {
		t.Errorf("InstallStaged() = %q; want %q", dst, want)
	}
	if got, err := os.ReadFile(dst); err != nil || string(got) != string(content) {
		t.Errorf("entry %s = %q, %v; want the staged binary %q", dst, got, err, content)
	}
	m, err := installedManifest(r)
	if err != nil || m == nil || m.Tag != "v1.0.0" || m.RepoURL != "https://example.com/mf-hello" {
		t.Errorf("Manifest(hello) = %+v, %v; want the installed v1.0.0 manifest", m, err)
	}
	assertBinDir(t, dir, "mf-hello"+hostExt())
	assertPkgDir(t, dir, "hello")
}

// installedManifest is the manifest of the Plugin hello resolves to, or
// nil when it has none.
func installedManifest(r *registry.Registry) (*registry.Manifest, error) {
	p, ok := r.Resolve("hello")
	if !ok {
		return nil, errors.New("hello does not resolve")
	}
	return p.Manifest, nil
}

// assertPkgDir checks the registry's pkg dir holds exactly want: no
// leftover staging directory.
func assertPkgDir(t *testing.T, dir string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "pkg"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if !slices.Equal(got, want) {
		t.Errorf("pkg dir = %v; want %v", got, want)
	}
}

func TestInstallStagedRefusesExistingWithoutForce(t *testing.T) {
	t.Parallel()
	r, dir := hostRegistry(t)
	first, err := r.InstallStaged(stage(t), manifest("v1.0.0"), false)
	if err != nil {
		t.Fatal(err)
	}
	staged := stage(t)

	if _, err := r.InstallStaged(staged, manifest("v2.0.0"), false); err == nil || !strings.Contains(err.Error(), first) {
		t.Fatalf("InstallStaged without force err = %v; want refusal naming %s", err, first)
	}
	if m, err := installedManifest(r); err != nil || m.Tag != "v1.0.0" {
		t.Errorf("Manifest(hello) = %+v, %v; want the first install kept", m, err)
	}
	if _, err := os.Stat(staged); err != nil {
		t.Errorf("a refused install consumed the staged binary: %v", err)
	}
	assertPkgDir(t, dir, "hello")
}

func TestInstallStagedForceReplacesLocalInstall(t *testing.T) {
	t.Parallel()
	r, dir := hostRegistry(t)
	local := writeExecutable(t, filepath.Join(t.TempDir(), "mf-hello"+hostExt()))
	if _, err := r.InstallLocal(local, "hello", false); err != nil {
		t.Fatal(err)
	}
	staged := stage(t)
	content, err := os.ReadFile(staged)
	if err != nil {
		t.Fatal(err)
	}

	dst, err := r.InstallStaged(staged, manifest("v2.0.0"), true)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(dst); err != nil || string(got) != string(content) {
		t.Errorf("entry %s = %q, %v; want the staged binary", dst, got, err)
	}
	if m, err := installedManifest(r); err != nil || m.Tag != "v2.0.0" {
		t.Errorf("Manifest(hello) = %+v, %v; want v2.0.0", m, err)
	}
	if _, err := os.Stat(local); err != nil {
		t.Errorf("replacing a local install touched its source: %v", err)
	}
	assertBinDir(t, dir, "mf-hello"+hostExt())
	assertPkgDir(t, dir, "hello")
}

func TestInstallStagedFailureWithForceKeepsOldPlugin(t *testing.T) {
	t.Parallel()
	r, dir := hostRegistry(t)
	first := stage(t)
	content, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	dst, err := r.InstallStaged(first, manifest("v1.0.0"), false)
	if err != nil {
		t.Fatal(err)
	}

	notExecutable := filepath.Join(t.TempDir(), "extracted-mf-hello")
	if err := os.WriteFile(notExecutable, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{filepath.Join(t.TempDir(), "missing"), notExecutable, t.TempDir()} {
		if _, err := r.InstallStaged(bad, manifest("v2.0.0"), true); err == nil {
			t.Errorf("InstallStaged(%s) with force = nil error; want failure", bad)
		}
	}
	if got, err := os.ReadFile(dst); err != nil || string(got) != string(content) {
		t.Errorf("entry %s = %q, %v; want the old plugin kept", dst, got, err)
	}
	if m, err := installedManifest(r); err != nil || m.Tag != "v1.0.0" {
		t.Errorf("Manifest(hello) = %+v, %v; want the old manifest kept", m, err)
	}
	assertBinDir(t, dir, "mf-hello"+hostExt())
	assertPkgDir(t, dir, "hello")
}

// A forced reinstall replaces the package's files in place, so the old entry
// never points at a missing binary: a failure clearing out the previous
// package still leaves hello runnable.
func TestInstallStagedForceKeepsEntryRunnableWhenCleanupFails(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs Unix permissions that bind the current user")
	}
	r, dir := hostRegistry(t)
	dst, err := r.InstallStaged(stage(t), manifest("v1.0.0"), false)
	if err != nil {
		t.Fatal(err)
	}
	// An undeletable leftover in the previous package.
	locked := filepath.Join(dir, "pkg", "hello", "locked")
	if err := os.MkdirAll(filepath.Join(locked, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(locked, 0o750); err != nil { // so TempDir cleanup can remove it
			t.Error(err)
		}
	})
	staged := stage(t)
	content, err := os.ReadFile(staged)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := r.InstallStaged(staged, manifest("v2.0.0"), true); err == nil {
		t.Error("InstallStaged with an undeletable leftover = nil error; want the cleanup failure reported")
	}
	if got, err := os.ReadFile(dst); err != nil || string(got) != string(content) {
		t.Errorf("entry %s = %q, %v; want it to run the new binary", dst, got, err)
	}
	if m, err := installedManifest(r); err != nil || m == nil || m.Tag != "v2.0.0" {
		t.Errorf("Manifest(hello) = %+v, %v; want v2.0.0", m, err)
	}
}

func TestInstallStagedRejectsBadManifest(t *testing.T) {
	t.Parallel()
	r, dir := hostRegistry(t)
	if _, err := r.InstallStaged(stage(t), nil, false); err == nil {
		t.Error("InstallStaged(nil manifest) = nil error; want rejection")
	}
	bad := manifest("v1.0.0")
	bad.Name = "../escape"
	if _, err := r.InstallStaged(stage(t), bad, false); err == nil {
		t.Error("InstallStaged(invalid name) = nil error; want rejection")
	}
	assertBinDir(t, dir)
	assertPkgDir(t, dir)
}

// With a relative MF_PLUGIN_DIR the entry must still point at the package,
// not at a relative path that dangles from inside bin/.
func TestInstallStagedWithRelativeDirResolvesAndRuns(t *testing.T) {
	t.Chdir(t.TempDir())
	r := registry.New(registry.Env{PluginDir: "plugins", GOOS: runtime.GOOS})
	if _, err := r.InstallStaged(stage(t), manifest("v1.0.0"), false); err != nil {
		t.Fatal(err)
	}

	p, ok := r.Resolve("hello")
	if !ok {
		t.Fatal("Resolve(hello) found nothing after a staged install into a relative registry dir")
	}
	if runtime.GOOS == "windows" {
		return
	}
	if out, err := exec.Command(p.Path).CombinedOutput(); err != nil {
		t.Errorf("run %s: %v: %s", p.Path, err, out)
	}
}
