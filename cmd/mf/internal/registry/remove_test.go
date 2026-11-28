package registry_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mguilarducci/mf-cli/cmd/mf/internal/registry"
)

// installWithData installs hello plus a package and a data dir, the full
// footprint of a Managed plugin.
func installWithData(t *testing.T) (r *registry.Registry, dir, data string) {
	t.Helper()
	r, dir = hostRegistry(t)
	src := writeExecutable(t, filepath.Join(t.TempDir(), "mf-hello"+hostExt()))
	if _, err := r.InstallLocal(src, "hello", false); err != nil {
		t.Fatal(err)
	}
	data, err := r.DataDir("hello")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{data, filepath.Join(dir, "pkg", "hello")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	return r, dir, data
}

func TestRemoveKeepsData(t *testing.T) {
	t.Parallel()
	r, dir, data := installWithData(t)

	if err := r.Remove("hello", false); err != nil {
		t.Fatal(err)
	}
	assertBinDir(t, dir)
	assertGone(t, filepath.Join(dir, "pkg", "hello"))
	if _, err := os.Stat(data); err != nil {
		t.Errorf("data dir should survive remove without purge: %v", err)
	}
}

func TestRemovePurgeRemovesData(t *testing.T) {
	t.Parallel()
	r, dir, data := installWithData(t)

	if err := r.Remove("hello", true); err != nil {
		t.Fatal(err)
	}
	assertBinDir(t, dir)
	assertGone(t, filepath.Join(dir, "pkg", "hello"))
	assertGone(t, data)
}

func TestRemoveMissingPluginErrors(t *testing.T) {
	t.Parallel()
	r, dir := hostRegistry(t)

	err := r.Remove("hello", false)
	if !errors.Is(err, registry.ErrNotManaged) || !strings.Contains(err.Error(), dir) {
		t.Errorf("Remove(missing) err = %v; want ErrNotManaged naming %s", err, dir)
	}
	if err := r.Remove("../escape", true); err == nil {
		t.Error("Remove(invalid name) = nil; want error")
	}
	if err := registry.New(registry.Env{}).Remove("hello", false); !errors.Is(err, registry.ErrUnavailable) {
		t.Errorf("Remove on unavailable registry err = %v; want ErrUnavailable", err)
	}
}

func assertGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s should be gone (stat err %v)", path, err)
	}
}
