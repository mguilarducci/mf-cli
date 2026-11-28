package cli

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/mguilarducci/mf-cli/cmd/mf/internal/registry"
)

// writeScript writes an executable script at path (path.bat on Windows) and
// returns the path it wrote. The ForkLock read lock keeps a parallel fork
// from inheriting the open file, which would make exec fail with ETXTBSY
// (golang/go#22315), as entireio/cli's writeExecutableScript does.
func writeScript(t *testing.T, path, unixBody, windowsBody string) string {
	t.Helper()
	body := "#!/bin/sh\n" + unixBody
	if runtime.GOOS == windowsGOOS {
		path += ".bat"
		body = "@echo off\r\n" + windowsBody
	}
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunPluginDataDirFromRegistry(t *testing.T) {
	binDir := t.TempDir()
	writeScript(t, filepath.Join(binDir, "mf-hello"),
		"printf '%s' \"$MF_PLUGIN_DATA_DIR\" > \"$1\"\n",
		"echo %MF_PLUGIN_DATA_DIR%> \"%~1\"\r\n")
	t.Setenv("MF_PLUGIN_DIR", t.TempDir()) // must not be consulted: the registry is the source

	regDir := t.TempDir()
	reg := registry.New(registry.Env{PluginDir: regDir, Path: []string{binDir}, GOOS: runtime.GOOS})
	out := filepath.Join(t.TempDir(), "out")

	handled, code := MaybeRunPlugin(context.Background(), reg, NewRootCmd(reg), []string{"hello", out})
	if !handled || code != 0 {
		t.Fatalf("MaybeRunPlugin = (%v, %d); want (true, 0)", handled, code)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(regDir, "data", "hello"); strings.TrimSpace(string(got)) != want {
		t.Errorf("MF_PLUGIN_DATA_DIR = %q; want %q", got, want)
	}
}

func TestRunPluginRunsWhatRegistryResolves(t *testing.T) {
	t.Parallel()
	regDir, pathDir := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(regDir, "bin"), 0o750); err != nil {
		t.Fatal(err)
	}
	writeScript(t, filepath.Join(regDir, "bin", "mf-hello"),
		"printf 'managed\\n%s\\n' \"$PATH\" > \"$1\"\n",
		"echo managed> \"%~1\"\r\necho %PATH%>> \"%~1\"\r\n")
	writeScript(t, filepath.Join(pathDir, "mf-hello"), "echo path > \"$1\"\n", "echo path> \"%~1\"\r\n")
	reg := registry.New(registry.Env{PluginDir: regDir, Path: []string{pathDir}, GOOS: runtime.GOOS})
	out := filepath.Join(t.TempDir(), "out")

	handled, code := MaybeRunPlugin(context.Background(), reg, NewRootCmd(reg), []string{"hello", out})
	if !handled || code != 0 {
		t.Fatalf("MaybeRunPlugin = (%v, %d); want (true, 0)", handled, code)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(got), "\r", "")), "\n")
	if len(lines) != 2 || lines[0] != "managed" || strings.TrimSpace(lines[1]) != reg.SearchPath() {
		t.Errorf("plugin wrote %q; want managed and PATH %q", got, reg.SearchPath())
	}
}
