// Portions adapted from entireio/cli (https://github.com/entireio/cli),
// Copyright (c) 2026 Entire Inc., MIT License.
// See third_party/licenses/entireio-cli.LICENSE.

package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/mguilarducci/mf-cli/cmd/mf/internal/registry"
)

// Remote plugin sources are git repositories. Tags and metadata are read with
// the git CLI, which behaves the same on every forge and reuses the user's
// credentials for private repos.

// gitURLSchemes are the URL schemes accepted for plugin and index repos.
// file:// is test-only, behind MF_PLUGIN_ALLOW_FILE_REMOTES=1.
//
//nolint:gochecknoglobals // static list
var gitURLSchemes = []string{"https://", "ssh://", "git+ssh://"}

// scpLikeGitURL matches git's user@host:path form.
var scpLikeGitURL = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[^/].*$`)

func allowLocalGitURL() bool {
	return os.Getenv("MF_PLUGIN_ALLOW_FILE_REMOTES") == "1"
}

func validatePluginRepoURL(rawURL string) error {
	u := strings.TrimSpace(rawURL)
	switch {
	case u == "":
		return errors.New("repository URL is empty")
	case hasControlChars(u):
		return fmt.Errorf("repository URL %q must not contain control characters", rawURL)
	case strings.HasPrefix(u, "-"):
		return fmt.Errorf("repository URL %q must not start with '-'", redactURL(rawURL))
	}
	for _, scheme := range gitURLSchemes {
		if strings.HasPrefix(u, scheme) && len(u) > len(scheme) {
			return nil
		}
	}
	if strings.HasPrefix(u, "file://") && len(u) > len("file://") && allowLocalGitURL() {
		return nil
	}
	if scpLikeGitURL.MatchString(u) {
		return nil
	}
	return fmt.Errorf("repository URL %q must use one of %s or git's user@host:path form",
		redactURL(rawURL), strings.Join(gitURLSchemes, ", "))
}

// hasControlChars reports characters a terminal acts on rather than displays.
// Remote-sourced strings (index entries, URLs) are rejected, not stripped.
func hasControlChars(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) ||
			(r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
	})
}

// redactURL drops credentials from a URL before it is printed.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = nil
	return u.String()
}

func runGit(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	// Never prompt: a missing credential must fail, not hang a non-interactive run.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return nil, fmt.Errorf("git %s: %w: %s", args[0], err, detail)
		}
		return nil, fmt.Errorf("git %s: %w", args[0], err)
	}
	return out, nil
}

// listRemoteSemverTags returns the repo's stable semver tags ("v"-prefixed or
// bare), newest first. Prereleases are skipped; --pin installs one.
func listRemoteSemverTags(ctx context.Context, repoURL string) ([]string, error) {
	if err := validatePluginRepoURL(repoURL); err != nil {
		return nil, err
	}
	out, err := runGit(ctx, "ls-remote", "--tags", "--refs", "--", repoURL)
	if err != nil {
		return nil, fmt.Errorf("list tags of %s: %w", redactURL(repoURL), err)
	}
	var tags []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		tag := strings.TrimPrefix(fields[1], "refs/tags/")
		canon := canonicalSemver(tag)
		if !semver.IsValid(canon) || semver.Prerelease(canon) != "" {
			continue
		}
		tags = append(tags, tag)
	}
	sort.SliceStable(tags, func(i, j int) bool {
		return semver.Compare(canonicalSemver(tags[i]), canonicalSemver(tags[j])) > 0
	})
	return tags, nil
}

func canonicalSemver(tag string) string {
	if strings.HasPrefix(tag, "v") {
		return tag
	}
	return "v" + tag
}

// fetchPluginMetadataAtTag reads mf-plugin.yml from the repo at tag. A repo
// without the file returns (nil, nil): metadata is optional.
func fetchPluginMetadataAtTag(ctx context.Context, repoURL, tag string) (*PluginMetadata, error) {
	if err := validatePluginRepoURL(repoURL); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "mf-plugin-meta-")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	// Blobless and checkout-free: only the one file is read.
	if _, err := runGit(ctx, "clone", "--depth", "1", "--no-checkout", "--filter=blob:none",
		"--branch", tag, "--quiet", "--", repoURL, tmp); err != nil {
		return nil, fmt.Errorf("clone %s at %s: %w", redactURL(repoURL), tag, err)
	}
	out, err := runGit(ctx, "-C", tmp, "show", "HEAD:"+pluginMetadataFileName)
	if err != nil {
		return nil, nil //nolint:nilnil // no mf-plugin.yml at this tag is valid
	}
	return ParsePluginMetadata(out)
}

// pluginNameFromRepoURL derives a plugin name from a repo named mf-<name>.
func pluginNameFromRepoURL(repoURL string) (string, error) {
	trimmed := strings.TrimSuffix(strings.TrimSuffix(repoURL, "/"), ".git")
	base := trimmed
	if i := strings.LastIndexAny(trimmed, "/:"); i >= 0 {
		base = trimmed[i+1:]
	}
	if !strings.HasPrefix(base, pluginBinaryPrefix) {
		return "", fmt.Errorf("repository basename %q does not start with %q; declare a name in %s",
			base, pluginBinaryPrefix, pluginMetadataFileName)
	}
	name := strings.TrimPrefix(base, pluginBinaryPrefix)
	if err := registry.ValidateName(name); err != nil {
		return "", err
	}
	return name, nil
}
