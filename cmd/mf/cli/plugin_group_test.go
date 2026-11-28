package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mguilarducci/mf-cli/cmd/mf/internal/registry"
)

// runMF runs the mf command tree against reg and returns its stderr+stdout.
func runMF(t *testing.T, reg *registry.Registry, args ...string) (string, error) {
	t.Helper()
	root := NewRootCmd(reg)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestPluginInstallAndRemoveUseRegistry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	reg := registry.New(registry.Env{PluginDir: dir, GOOS: runtime.GOOS})
	src := writeExecutable(t, filepath.Join(t.TempDir(), "mf-hello"))

	out, err := runMF(t, reg, "plugin", "install", src)
	if err != nil {
		t.Fatalf("install: %v (%s)", err, out)
	}
	entry := filepath.Join(dir, "bin", filepath.Base(src))
	if !strings.Contains(out, entry) {
		t.Errorf("install output %q should name the entry %s", out, entry)
	}
	if _, err := os.Lstat(entry); err != nil {
		t.Fatalf("install did not place %s: %v", entry, err)
	}
	if _, err := runMF(t, reg, "plugin", "install", src); err == nil || !strings.Contains(err.Error(), entry) {
		t.Errorf("reinstall without --force err = %v; want refusal naming %s", err, entry)
	}

	if out, err := runMF(t, reg, "plugin", "remove", "hello"); err != nil {
		t.Fatalf("remove: %v (%s)", err, out)
	}
	if _, err := os.Lstat(entry); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("remove left %s behind (lstat err %v)", entry, err)
	}
	if _, err := runMF(t, reg, "plugin", "remove", "hello"); !errors.Is(err, registry.ErrNotManaged) {
		t.Errorf("removing a missing plugin err = %v; want ErrNotManaged", err)
	}
}
