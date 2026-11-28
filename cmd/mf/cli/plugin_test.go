package cli

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mguilarducci/mf-cli/cmd/mf/internal/registry"
)

func TestPluginNameFromPath(t *testing.T) {
	cases := map[string]string{
		"./bin/mf-digisac": "digisac",
		"/x/mf-my-tool":    "my-tool",
		"plain":            "plain",
	}
	for in, want := range cases {
		if got := pluginNameFromPath(in); got != want {
			t.Errorf("pluginNameFromPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPluginEnv(t *testing.T) {
	parent := []string{
		"PATH=/bin",
		"HOME=/home/u",
		"DIGISAC_TOKEN=tok",
		"MF_DEBUG=1",
		"XDG_CONFIG_HOME=/cfg",
		"AWS_SECRET_ACCESS_KEY=secret",
		"OTHER_TOKEN=nope",
		"GITHUB_TOKEN=gh",
		"MF_PLUGIN_ENV=AWS_PROFILE,GITHUB_*",
		"AWS_PROFILE=dev",
		"malformed",
	}
	got := pluginEnv(parent, "digisac", "MF_PLUGIN_NAME=digisac")

	for _, want := range []string{
		"PATH=/bin", "HOME=/home/u", "DIGISAC_TOKEN=tok", "MF_DEBUG=1",
		"XDG_CONFIG_HOME=/cfg", "GITHUB_TOKEN=gh", "AWS_PROFILE=dev",
		"MF_PLUGIN_NAME=digisac",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("pluginEnv missing %q", want)
		}
	}
	for _, leaked := range []string{"AWS_SECRET_ACCESS_KEY", "OTHER_TOKEN", "malformed"} {
		for _, kv := range got {
			if strings.HasPrefix(kv, leaked) {
				t.Errorf("pluginEnv leaked %q", kv)
			}
		}
	}
}

func TestPluginEnvPrefix(t *testing.T) {
	cases := map[string]string{"digisac": "DIGISAC_", "my-tool": "MY_TOOL_"}
	for in, want := range cases {
		if got := pluginEnvPrefix(in); got != want {
			t.Errorf("pluginEnvPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPluginBinDir(t *testing.T) {
	t.Setenv("MF_PLUGIN_DIR", "")
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	if got, err := PluginBinDir(); err != nil || got != filepath.Join(xdg, "mf", "plugins", "bin") {
		t.Errorf("PluginBinDir() = %q, %v", got, err)
	}
}

// writeExecutable creates a tiny no-op plugin at path (path.bat on Windows)
// and returns the path it wrote.
func writeExecutable(t *testing.T, path string) string {
	t.Helper()
	return writeScript(t, path, "exit 0\n", "exit /b 0\r\n")
}

func TestResolvePlugin(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "mf-hello"))
	writeExecutable(t, filepath.Join(binDir, "mf-plugin")) // shadowed by built-in
	t.Setenv("PATH", binDir)

	root := NewRootCmd(registry.New(registry.Env{}))
	cases := []struct {
		args     []string
		ok       bool
		wantArgs []string
	}{
		{[]string{"hello", "a", "--b"}, true, []string{"a", "--b"}},
		{[]string{"plugin", "list"}, false, nil}, // built-in wins
		{[]string{"help"}, false, nil},
		{[]string{"missing"}, false, nil},
		{[]string{"--version"}, false, nil},
		{nil, false, nil},
	}
	for _, c := range cases {
		_, args, ok := resolvePlugin(root, c.args)
		if ok != c.ok || (ok && !slices.Equal(args, c.wantArgs)) {
			t.Errorf("resolvePlugin(%v) = (%v, %v), want (%v, %v)", c.args, args, ok, c.wantArgs, c.ok)
		}
	}
}

func TestFormatPluginHelpSection(t *testing.T) {
	root := NewRootCmd(registry.New(registry.Env{}))
	got := formatPluginHelpSection(root, []pluginEntry{{Name: "digisac"}, {Name: "plugin"}})
	if !strings.Contains(got, "digisac") || !strings.Contains(got, "Plugin Commands:") {
		t.Errorf("help section = %q", got)
	}
	if strings.Contains(got, "mf-plugin") {
		t.Errorf("help section should hide plugins shadowed by built-ins: %q", got)
	}
	if formatPluginHelpSection(root, nil) != "" {
		t.Error("empty plugin list should render nothing")
	}
}
