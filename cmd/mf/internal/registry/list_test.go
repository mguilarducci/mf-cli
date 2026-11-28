package registry_test

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/mguilarducci/mf-cli/cmd/mf/internal/registry"
)

func names(ps []registry.Plugin) []string {
	out := []string{}
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

func TestListSortedFirstMatchWins(t *testing.T) {
	t.Parallel()
	regDir, first, second := t.TempDir(), t.TempDir(), t.TempDir()
	managed := writePlugin(t, filepath.Join(regDir, "bin"), "zeta")
	writePlugin(t, first, "zeta")
	alpha := writePlugin(t, first, "alpha")
	writePlugin(t, second, "alpha")
	writePlugin(t, second, "mid")
	writePlugin(t, second, "Bad")
	writeFile(t, second, "not-a-plugin", 0o755)
	r := registry.New(registry.Env{PluginDir: regDir, Path: []string{first, second}, GOOS: runtime.GOOS})

	got := r.List()
	if want := []string{"alpha", "mid", "zeta"}; !slices.Equal(names(got), want) {
		t.Fatalf("List() names = %v; want %v", names(got), want)
	}
	if got[0].Path != alpha || got[0].Managed {
		t.Errorf("alpha = %+v; want unmanaged %s", got[0], alpha)
	}
	if got[2].Path != managed || !got[2].Managed {
		t.Errorf("zeta = %+v; want managed %s", got[2], managed)
	}
	for _, p := range got {
		if resolved, ok := r.Resolve(p.Name); !ok || resolved.Path != p.Path || resolved.Managed != p.Managed || resolved.Target != p.Target {
			t.Errorf("Resolve(%s) = %+v, %v; List has %+v", p.Name, resolved, ok, p)
		}
	}
}

func TestListSkipsRelativeAndEmptyPathEntries(t *testing.T) {
	t.Parallel()
	planted := filepath.Join(t.TempDir(), "bin")
	writePlugin(t, planted, "hello")
	r := registry.New(registry.Env{Path: []string{"", ".", relativeTo(t, planted)}, GOOS: runtime.GOOS})

	if got := r.List(); len(got) != 0 {
		t.Errorf("List() = %+v; want nothing through relative PATH entries", got)
	}
}

func TestListWindowsPicksByPathExtOrder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "mf-hello.bat", 0o644)
	exe := writeFile(t, dir, "mf-hello.exe", 0o644)
	writeFile(t, dir, "mf-other.txt", 0o644)
	r := registry.New(registry.Env{Path: []string{dir}, GOOS: "windows"})

	got := r.List()
	if len(got) != 1 || got[0].Name != "hello" || got[0].Path != exe {
		t.Errorf("List() = %+v; want only hello at %s", got, exe)
	}
}

func TestSearchPathPutsRegistryFirst(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		env  registry.Env
		want string
	}{
		{"unix", registry.Env{PluginDir: "/reg", Path: []string{"/usr/bin", "", "bin", "/bin"}, GOOS: "linux"},
			filepath.Join("/reg", "bin") + ":/usr/bin::bin:/bin"},
		{"windows", registry.Env{PluginDir: `C:\reg`, Path: []string{`C:\Windows`, `C:\tools`}, GOOS: "windows"},
			filepath.Join(`C:\reg`, "bin") + `;C:\Windows;C:\tools`},
		{"unavailable registry keeps PATH", registry.Env{Path: []string{"/usr/bin", "/bin"}, GOOS: "linux"},
			"/usr/bin:/bin"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := registry.New(c.env).SearchPath(); got != c.want {
				t.Errorf("SearchPath() = %q; want %q", got, c.want)
			}
		})
	}
}

func TestManagedPluginCarriesManifest(t *testing.T) {
	t.Parallel()
	regDir, pathDir := t.TempDir(), t.TempDir()
	writePlugin(t, filepath.Join(regDir, "bin"), "remote")
	writePlugin(t, filepath.Join(regDir, "bin"), "local")
	writePlugin(t, pathDir, "plain")
	writeFile(t, filepath.Join(regDir, "pkg", "remote"), "manifest.yml", 0o600)
	manifest := "name: remote\nrepo_url: https://github.com/o/mf-remote\ntag: v1.2.3\n"
	if err := os.WriteFile(filepath.Join(regDir, "pkg", "remote", "manifest.yml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	r := registry.New(registry.Env{PluginDir: regDir, Path: []string{pathDir}, GOOS: runtime.GOOS})

	p, ok := r.Resolve("remote")
	if !ok || p.Manifest == nil || p.Manifest.RepoURL != "https://github.com/o/mf-remote" || p.Manifest.Tag != "v1.2.3" {
		t.Fatalf("Resolve(remote) = %+v, %v; want its manifest", p, ok)
	}
	for _, p := range r.List() {
		if (p.Manifest != nil) != (p.Name == "remote") {
			t.Errorf("List() %s manifest = %+v", p.Name, p.Manifest)
		}
	}
}
