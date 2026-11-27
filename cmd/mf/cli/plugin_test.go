package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestValidatePluginName(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"digisac", true},
		{"my-tool", true},
		{"tool_2", true},
		{"", false},
		{"-flag", false},
		{"--help", false},
		{"Upper", false},
		{"a/b", false},
		{"..", false},
		{"with space", false},
	}
	for _, c := range cases {
		if err := validatePluginName(c.name); (err == nil) != c.ok {
			t.Errorf("validatePluginName(%q) err = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}

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

func TestPluginDirs(t *testing.T) {
	t.Setenv("MF_PLUGIN_DIR", "")
	t.Setenv("XDG_DATA_HOME", "/xdg")
	if got, _ := PluginBinDir(); got != "/xdg/mf/plugins/bin" {
		t.Errorf("PluginBinDir() = %q", got)
	}
	t.Setenv("MF_PLUGIN_DIR", "/custom")
	if got, _ := PluginDataDir("digisac"); got != "/custom/data/digisac" {
		t.Errorf("PluginDataDir() = %q", got)
	}
	if _, err := PluginDataDir("../escape"); err == nil {
		t.Error("PluginDataDir(\"../escape\"): want error")
	}
}

// writeExecutable creates a tiny shell script plugin.
func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestInstallListRemove(t *testing.T) {
	pluginDir := t.TempDir()
	t.Setenv("MF_PLUGIN_DIR", pluginDir)
	t.Setenv("PATH", "")
	PrependPluginBinDirToPATH()

	src := filepath.Join(t.TempDir(), "mf-hello")
	writeExecutable(t, src)

	dst, err := installLocalPlugin(src, pluginNameFromPath(src))
	if err != nil {
		t.Fatal(err)
	}
	if target, _ := os.Readlink(dst); target != src {
		t.Errorf("install should symlink to %q, got %q", src, target)
	}

	plugins := discoverPlugins()
	if len(plugins) != 1 || plugins[0].Name != "hello" || !plugins[0].Managed {
		t.Fatalf("discoverPlugins() = %+v", plugins)
	}

	// Reinstalling replaces the entry instead of failing.
	if _, err := installLocalPlugin(src, "hello"); err != nil {
		t.Fatalf("reinstall: %v", err)
	}

	if err := removePlugin("hello", false); err != nil {
		t.Fatal(err)
	}
	if got := discoverPlugins(); len(got) != 0 {
		t.Errorf("after remove, discoverPlugins() = %+v", got)
	}
	if err := removePlugin("hello", false); err == nil {
		t.Error("removing a missing plugin: want error")
	}
}

func TestInstallRejectsNonExecutable(t *testing.T) {
	t.Setenv("MF_PLUGIN_DIR", t.TempDir())
	src := filepath.Join(t.TempDir(), "mf-data")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installLocalPlugin(src, "data"); err == nil {
		t.Error("want error for non-executable file")
	}
}

func TestResolvePlugin(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "mf-hello"))
	writeExecutable(t, filepath.Join(binDir, "mf-plugin")) // shadowed by built-in
	t.Setenv("PATH", binDir)

	root := NewRootCmd()
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
	root := NewRootCmd()
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
