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
	t.Chdir(t.TempDir())
	if err := os.MkdirAll("bin", 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("bin/mf-local", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		arg     string
		kind    installSourceKind
		ref     string
		name    string
		wantErr bool
	}{
		{arg: "digisac", kind: installFromRepo, ref: "https://github.com/mguilarducci/mf-digisac", name: "digisac"},
		{arg: "someone/foo", kind: installFromRepo, ref: "https://github.com/someone/mf-foo", name: "foo"},
		{arg: "someone/mf-foo", kind: installFromRepo, ref: "https://github.com/someone/mf-foo", name: "foo"},
		{arg: "https://github.com/mguilarducci/mf-digisac", kind: installFromURL, ref: "https://github.com/mguilarducci/mf-digisac"},
		{arg: "git@github.com:mguilarducci/mf-digisac.git", kind: installFromURL, ref: "git@github.com:mguilarducci/mf-digisac.git"},
		{arg: "./bin/mf-digisac", kind: installFromPath, ref: "./bin/mf-digisac"},
		{arg: "../mf-digisac/bin/mf-digisac", kind: installFromPath, ref: "../mf-digisac/bin/mf-digisac"},
		{arg: "bin/mf-local", wantErr: true}, // a local file and an owner/name: ambiguous
		{arg: "a/b/c", wantErr: true},        // neither owner/name nor an explicit path
		{arg: "-x", wantErr: true},
		{arg: "Bad Name", wantErr: true},
		{arg: "someone/Bad", wantErr: true},
		{arg: "ftp://example.com/x", wantErr: true},
	}
	for _, c := range cases {
		src, err := parseInstallSource(c.arg)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseInstallSource(%q) = %+v; want error", c.arg, src)
			}
			continue
		}
		if err != nil || src.Kind != c.kind || src.Ref != c.ref || src.Name != c.name {
			t.Errorf("parseInstallSource(%q) = %+v, %v; want kind=%v ref=%q name=%q", c.arg, src, err, c.kind, c.ref, c.name)
		}
	}
}

func TestTrustedRepo(t *testing.T) {
	for in, want := range map[string]bool{
		"https://github.com/mguilarducci/mf-digisac":      true,
		"https://github.com/mguilarducci/mf-digisac.git/": true,
		"https://github.com/someone/mf-foo":               false,
		"https://github.com/mguilarducci-evil/mf-foo":     false,
		"git@github.com:mguilarducci/mf-digisac.git":      false, // not the https form mf builds
	} {
		if got := trustedRepo(in); got != want {
			t.Errorf("trustedRepo(%q) = %v, want %v", in, got, want)
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
	cmd := exec.CommandContext(t.Context(), "git", args...)
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
			if _, err := w.Write(archive); err != nil {
				t.Errorf("serve asset: %v", err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func setupRemote(t *testing.T, corrupt bool) (repoURL string) {
	t.Helper()
	t.Setenv("MF_PLUGIN_ALLOW_FILE_REMOTES", "1")
	t.Setenv("MF_PLUGIN_DIR", t.TempDir())
	srv := releaseServer(t, "hello", []byte("#!/bin/sh\necho hello\n"), corrupt)
	return gitRepo(t, map[string]string{
		pluginMetadataFileName: "name: hello\ndescription: Says hello\ndownload_url: " + srv.URL + "/{tag}/{asset}\n",
	}, "v0.9.0", "v1.0.0", "v1.1.0-rc.1")
}

func TestInstallFromRepoEndToEnd(t *testing.T) {
	repoURL := setupRemote(t, false)
	ctx := context.Background()

	m, err := InstallPluginFromRepo(ctx, repoURL, "hello", RemoteInstallOptions{})
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

	if _, err := InstallPluginFromRepo(ctx, repoURL, "hello", RemoteInstallOptions{}); err == nil {
		t.Error("reinstall without --force: want error")
	}
	m, err = InstallPluginFromRepo(ctx, repoURL, "hello", RemoteInstallOptions{Force: true, Pin: "v0.9.0"})
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
	repoURL := setupRemote(t, true)
	_, err := InstallPluginFromRepo(context.Background(), repoURL, "", RemoteInstallOptions{})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want checksum mismatch, got %v", err)
	}
	if got, _ := managedEntries("hello"); len(got) != 0 { //nolint:errcheck // dir resolution can't fail here
		t.Errorf("nothing should be installed after a failed verify: %v", got)
	}
}

func TestInstallRefusesUnexpectedName(t *testing.T) {
	repoURL := setupRemote(t, false)
	if _, err := InstallPluginFromRepo(context.Background(), repoURL, "other", RemoteInstallOptions{}); err == nil {
		t.Fatal("requested name differs from the declared name: want error")
	}
}

func TestResolveBareName(t *testing.T) {
	exists := map[string]bool{"https://github.com/mguilarducci/mf-digisac": true}
	orig := repoExists
	repoExists = func(_ context.Context, u string) bool { return exists[u] }
	t.Cleanup(func() { repoExists = orig })

	bare := func(name string) installSource {
		src, err := parseInstallSource(name)
		if err != nil || !src.Bare {
			t.Fatalf("parseInstallSource(%q) = %+v, %v", name, src, err)
		}
		return src
	}
	run := func(name, input string, interactive bool) (installSource, error) {
		cmd := newPluginInstallCmd()
		cmd.SetErr(&bytes.Buffer{})
		return resolveBareName(context.Background(), cmd, bare(name), strings.NewReader(input), interactive)
	}

	// Found under the default owner: no prompt, nothing read.
	if got, err := run("digisac", "", false); err != nil || got.Ref != "https://github.com/mguilarducci/mf-digisac" {
		t.Errorf("default owner hit = %+v, %v", got, err)
	}
	// Not found, interactive: the typed owner/repo is used and counts as answered.
	got, err := run("foo", "someone/foo\n", true)
	if err != nil || got.Ref != "https://github.com/someone/mf-foo" || got.Name != "foo" || !got.Answered {
		t.Errorf("owner/repo answer = %+v, %v", got, err)
	}
	// A full URL is accepted too.
	if got, err := run("foo", "https://gitlab.com/x/mf-foo\n", true); err != nil || got.Kind != installFromURL {
		t.Errorf("URL answer = %+v, %v", got, err)
	}
	// Empty answer cancels; a bare name or path answer is refused.
	for _, in := range []string{"\n", "foo\n", "./mf-foo\n"} {
		if _, err := run("foo", in, true); err == nil {
			t.Errorf("answer %q: want error", in)
		}
	}
	// Not found, non-interactive: fail and say to pass owner/repo.
	if _, err := run("foo", "", false); err == nil || !strings.Contains(err.Error(), "pass owner/repo") {
		t.Errorf("non-interactive miss = %v", err)
	}
}
