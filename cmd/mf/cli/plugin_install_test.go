package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestParseInstallSource(t *testing.T) {
	cases := []struct {
		arg  string
		kind installSourceKind
		ok   bool
	}{
		{"digisac", installFromIndex, true},
		{"https://github.com/mguilarducci/mf-digisac", installFromURL, true},
		{"git@github.com:mguilarducci/mf-digisac.git", installFromURL, true},
		{"./bin/mf-digisac", installFromPath, true},
		{"bin/mf-digisac", installFromPath, true},
		{".", installFromPath, true},
		{"-x", 0, false},
		{"Bad Name", 0, false},
		{"ftp://example.com/x", 0, false},
	}
	for _, c := range cases {
		src, err := parseInstallSource(c.arg)
		if (err == nil) != c.ok || (c.ok && src.Kind != c.kind) {
			t.Errorf("parseInstallSource(%q) = %+v, %v; want kind %v ok=%v", c.arg, src, err, c.kind, c.ok)
		}
	}
}

func TestValidatePluginRepoURL(t *testing.T) {
	t.Setenv("MF_PLUGIN_ALLOW_FILE_REMOTES", "")
	for _, ok := range []string{"https://github.com/a/mf-b", "ssh://git@host/a/b", "git@github.com:a/b.git"} {
		if err := validatePluginRepoURL(ok); err != nil {
			t.Errorf("validatePluginRepoURL(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-https://x", "http://github.com/a/b", "file:///tmp/x", "https://x\x1b[2J"} {
		if err := validatePluginRepoURL(bad); err == nil {
			t.Errorf("validatePluginRepoURL(%q): want error", bad)
		}
	}
	t.Setenv("MF_PLUGIN_ALLOW_FILE_REMOTES", "1")
	if err := validatePluginRepoURL("file:///tmp/x"); err != nil {
		t.Errorf("file:// with MF_PLUGIN_ALLOW_FILE_REMOTES=1: %v", err)
	}
}

func TestPluginNameFromRepoURL(t *testing.T) {
	cases := map[string]string{
		"https://github.com/mguilarducci/mf-digisac":     "digisac",
		"https://github.com/mguilarducci/mf-digisac.git": "digisac",
		"git@github.com:mguilarducci/mf-digisac.git":     "digisac",
	}
	for in, want := range cases {
		if got, err := pluginNameFromRepoURL(in); err != nil || got != want {
			t.Errorf("pluginNameFromRepoURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := pluginNameFromRepoURL("https://github.com/a/digisac"); err == nil {
		t.Error("repo without mf- prefix: want error")
	}
}

func TestAssetCandidatesMatchGoReleaserNames(t *testing.T) {
	got := assetCandidates("digisac", "v0.1.0")
	ext := ".tar.gz"
	if runtime.GOOS == windowsGOOS {
		ext = ".zip"
	}
	// The name mf-digisac's .goreleaser.yaml publishes.
	want := fmt.Sprintf("mf-digisac_%s_%s%s", runtime.GOOS, runtime.GOARCH, ext)
	if !slices.Contains(got, want) {
		t.Errorf("assetCandidates missing %q; got %v", want, got)
	}
}

func TestParseChecksums(t *testing.T) {
	sums := parseChecksums([]byte("ABC  mf-x_linux_amd64.tar.gz\ndef *mf-x.zip\nbad line here\n"))
	if sums["mf-x_linux_amd64.tar.gz"] != "abc" || sums["mf-x.zip"] != "def" || len(sums) != 2 {
		t.Errorf("parseChecksums = %v", sums)
	}
}

func TestParsePluginIndexDropsInvalidEntries(t *testing.T) {
	idx, err := parsePluginIndex([]byte(`{"version":1,"plugins":[
		{"name":"digisac","repo_url":"https://github.com/mguilarducci/mf-digisac","description":"ok"},
		{"name":"Bad","repo_url":"https://github.com/a/b"},
		{"name":"evil","repo_url":"https://github.com/a/b","description":"\u001b[2J"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Plugins) != 1 || idx.Find("digisac") == nil {
		t.Errorf("index = %+v", idx.Plugins)
	}
	if idx.FindByRepoURL("https://github.com/mguilarducci/mf-digisac.git/") == nil {
		t.Error("FindByRepoURL should normalize .git and trailing slash")
	}
	if _, err := parsePluginIndex([]byte(`{"version":2,"plugins":[]}`)); err == nil {
		t.Error("unknown schema version: want error")
	}
}

func TestParsePluginMetadata(t *testing.T) {
	m, err := ParsePluginMetadata([]byte("name: digisac\ndescription: Digisac\nfuture_field: ignored\n"))
	if err != nil || m.Name != "digisac" || m.Description != "Digisac" {
		t.Errorf("ParsePluginMetadata = %+v, %v", m, err)
	}
	if _, err := ParsePluginMetadata([]byte("name: Bad Name\n")); err == nil {
		t.Error("invalid name: want error")
	}
}

func TestRequireSecureAssetURL(t *testing.T) {
	for _, ok := range []string{"https://github.com/x", "http://127.0.0.1:8080/x", "http://localhost/x"} {
		if err := requireSecureAssetURL(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://github.com/x", "ftp://x/y"} {
		if err := requireSecureAssetURL(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

// --- end to end: a git repo with a tag, an index repo, and a release server.

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func gitRepo(t *testing.T, files map[string]string, tags ...string) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "init")
	for _, tag := range tags {
		git(t, dir, "tag", tag)
	}
	return "file://" + filepath.ToSlash(dir)
}

func tarGz(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// releaseServer serves one release: checksums.txt plus this platform's archive.
func releaseServer(t *testing.T, name string, binary []byte, corruptChecksum bool) *httptest.Server {
	t.Helper()
	asset := fmt.Sprintf("mf-%s_%s_%s.tar.gz", name, runtime.GOOS, runtime.GOARCH)
	archive := tarGz(t, pluginBinaryName(name), binary)
	sum := sha256.Sum256(archive)
	digest := hex.EncodeToString(sum[:])
	if corruptChecksum {
		digest = strings.Repeat("0", 64)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch filepath.Base(r.URL.Path) {
		case checksumsFileName:
			fmt.Fprintf(w, "%s  %s\n", digest, asset)
		case asset:
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func setupRemote(t *testing.T, corrupt bool) (repoURL, indexURL string) {
	t.Helper()
	t.Setenv("MF_PLUGIN_ALLOW_FILE_REMOTES", "1")
	t.Setenv("MF_PLUGIN_DIR", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir()) // index cache (Linux)
	t.Setenv("HOME", t.TempDir())           // index cache (macOS)
	srv := releaseServer(t, "hello", []byte("#!/bin/sh\necho hello\n"), corrupt)
	repoURL = gitRepo(t, map[string]string{
		pluginMetadataFileName: "name: hello\ndescription: Says hello\ndownload_url: " + srv.URL + "/{tag}/{asset}\n",
	}, "v0.9.0", "v1.0.0", "v1.1.0-rc.1")
	indexURL = gitRepo(t, map[string]string{
		pluginIndexFileName: `{"version":1,"plugins":[{"name":"hello","repo_url":"` + repoURL + `"}]}`,
	})
	return repoURL, indexURL
}

func TestInstallFromIndexEndToEnd(t *testing.T) {
	_, indexURL := setupRemote(t, false)
	ctx := context.Background()

	idx, err := SyncPluginIndex(ctx, indexURL, false)
	if err != nil {
		t.Fatal(err)
	}
	entry := idx.Find("hello")
	if entry == nil {
		t.Fatal("index has no hello")
	}
	m, err := InstallPluginFromRepo(ctx, entry.RepoURL, entry.Name, RemoteInstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if m.Tag != "v1.0.0" || m.Unverified || m.Description != "Says hello" {
		t.Errorf("manifest = %+v; want newest stable tag v1.0.0, verified", m)
	}

	PrependPluginBinDirToPATH()
	plugins := discoverPlugins()
	if len(plugins) != 1 || plugins[0].Name != "hello" || plugins[0].Manifest == nil || plugins[0].Manifest.Tag != "v1.0.0" {
		t.Fatalf("discoverPlugins = %+v", plugins)
	}

	if _, err := InstallPluginFromRepo(ctx, entry.RepoURL, "hello", RemoteInstallOptions{}); err == nil {
		t.Error("reinstall without --force: want error")
	}
	m, err = InstallPluginFromRepo(ctx, entry.RepoURL, "hello", RemoteInstallOptions{Force: true, Pin: "v0.9.0"})
	if err != nil || m.Tag != "v0.9.0" || !m.Pinned {
		t.Errorf("pinned reinstall = %+v, %v", m, err)
	}

	if err := removePlugin("hello", false); err != nil {
		t.Fatal(err)
	}
	pkg, _ := PluginPkgDir("hello") //nolint:errcheck // name is valid
	if _, err := os.Stat(pkg); !os.IsNotExist(err) {
		t.Errorf("remove should delete %s", pkg)
	}
}

func TestInstallRejectsChecksumMismatch(t *testing.T) {
	repoURL, _ := setupRemote(t, true)
	_, err := InstallPluginFromRepo(context.Background(), repoURL, "", RemoteInstallOptions{})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want checksum mismatch, got %v", err)
	}
	if got, _ := managedEntries("hello"); len(got) != 0 { //nolint:errcheck // dir resolution can't fail here
		t.Errorf("nothing should be installed after a failed verify: %v", got)
	}
}

func TestInstallRefusesUnexpectedName(t *testing.T) {
	repoURL, _ := setupRemote(t, false)
	if _, err := InstallPluginFromRepo(context.Background(), repoURL, "other", RemoteInstallOptions{}); err == nil {
		t.Fatal("index name differs from declared name: want error")
	}
}
