package registry_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mguilarducci/mf-cli/cmd/mf/internal/registry"
)

func TestDirPrecedence(t *testing.T) {
	t.Parallel()
	pluginDir, xdg, home := t.TempDir(), t.TempDir(), t.TempDir()
	cases := []struct {
		name string
		env  registry.Env
		want string
	}{
		{"MF_PLUGIN_DIR wins", registry.Env{PluginDir: pluginDir, XDGDataHome: xdg, Home: home}, pluginDir},
		{"XDG_DATA_HOME next", registry.Env{XDGDataHome: xdg, Home: home}, filepath.Join(xdg, "mf", "plugins")},
		{"home last", registry.Env{Home: home}, filepath.Join(home, ".local", "share", "mf", "plugins")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := registry.New(c.env).Dir()
			if err != nil || got != c.want {
				t.Errorf("Dir() = %q, %v; want %q", got, err, c.want)
			}
		})
	}
}

func TestRelativeDirIsMadeAbsolute(t *testing.T) {
	t.Parallel()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got, err := registry.New(registry.Env{PluginDir: filepath.Join("rel", "plugins")}).Dir()
	if want := filepath.Join(cwd, "rel", "plugins"); err != nil || got != want {
		t.Errorf("Dir() = %q, %v; want %q", got, err, want)
	}
}

func TestUnavailableWithoutHome(t *testing.T) {
	t.Parallel()
	r := registry.New(registry.Env{Path: []string{t.TempDir()}})
	if _, err := r.Dir(); !errors.Is(err, registry.ErrUnavailable) {
		t.Errorf("Dir() err = %v; want ErrUnavailable", err)
	}
	if _, err := r.DataDir("hello"); !errors.Is(err, registry.ErrUnavailable) {
		t.Errorf("DataDir() err = %v; want ErrUnavailable", err)
	}
}

func TestDataDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := registry.New(registry.Env{PluginDir: dir})
	if got, err := r.DataDir("digisac"); err != nil || got != filepath.Join(dir, "data", "digisac") {
		t.Errorf("DataDir(digisac) = %q, %v", got, err)
	}
	for _, name := range []string{"", "../escape", "a/b", "..", "-flag", "Upper", "with space"} {
		if got, err := r.DataDir(name); err == nil {
			t.Errorf("DataDir(%q) = %q; want error", name, got)
		}
	}
}
