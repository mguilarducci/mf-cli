// Portions adapted from entireio/cli (https://github.com/entireio/cli),
// Copyright (c) 2026 Entire Inc., MIT License.
// See third_party/licenses/entireio-cli.LICENSE.

package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The plugin index maps bare names to repositories, so `mf plugin install
// digisac` resolves without a URL. It is itself a git repository holding
// index.json, shallow-cloned into the user cache and refreshed after a TTL.
const (
	defaultPluginIndexURL    = "https://github.com/mguilarducci/mf-plugin-index"
	pluginIndexEnvVar        = "MF_PLUGIN_INDEX_URL"
	pluginIndexFileName      = "index.json"
	pluginIndexSchemaVersion = 1
	pluginIndexSyncMarker    = ".mf-last-sync"
	maxPluginIndexSize       = 8 << 20
	pluginIndexTTL           = 24 * time.Hour
)

type PluginIndex struct {
	Version int                `json:"version"`
	Plugins []PluginIndexEntry `json:"plugins"`
}

type PluginIndexEntry struct {
	Name        string `json:"name"`
	RepoURL     string `json:"repo_url"`
	Description string `json:"description,omitempty"`
}

func (idx *PluginIndex) Find(name string) *PluginIndexEntry {
	if idx == nil {
		return nil
	}
	for i := range idx.Plugins {
		if idx.Plugins[i].Name == name {
			return &idx.Plugins[i]
		}
	}
	return nil
}

func (idx *PluginIndex) FindByRepoURL(repoURL string) *PluginIndexEntry {
	if idx == nil {
		return nil
	}
	want := normalizeRepoURL(repoURL)
	for i := range idx.Plugins {
		if normalizeRepoURL(idx.Plugins[i].RepoURL) == want {
			return &idx.Plugins[i]
		}
	}
	return nil
}

func normalizeRepoURL(u string) string {
	return strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(u), "/"), ".git")
}

// resolvePluginIndexURL: --index flag, then MF_PLUGIN_INDEX_URL, then default.
func resolvePluginIndexURL(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if v := os.Getenv(pluginIndexEnvVar); v != "" {
		return v
	}
	return defaultPluginIndexURL
}

func pluginIndexCacheDir(indexURL string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache dir: %w", err)
	}
	sum := sha256.Sum256([]byte(normalizeRepoURL(indexURL)))
	return filepath.Join(cache, "mf", "plugin-index", hex.EncodeToString(sum[:6])), nil
}

// SyncPluginIndex returns the index, cloning or refreshing the cache when it
// is missing, older than the TTL, or force is set. A failed refresh falls
// back to the cached copy.
func SyncPluginIndex(ctx context.Context, indexURL string, force bool) (*PluginIndex, error) {
	if err := validatePluginRepoURL(indexURL); err != nil {
		return nil, fmt.Errorf("plugin index URL: %w", err)
	}
	dir, err := pluginIndexCacheDir(indexURL)
	if err != nil {
		return nil, err
	}
	_, statErr := os.Stat(filepath.Join(dir, ".git"))
	cloned := statErr == nil

	fresh := false
	if cloned && !force {
		if info, err := os.Stat(filepath.Join(dir, pluginIndexSyncMarker)); err == nil {
			fresh = time.Since(info.ModTime()) < pluginIndexTTL
		}
	}

	switch {
	case fresh:
	case cloned:
		if _, err := runGit(ctx, "-C", dir, "pull", "--ff-only", "--depth", "1", "--quiet"); err == nil {
			touchIndexMarker(dir)
		} // else: keep the cached copy; it is better than nothing offline
	default:
		if err := os.MkdirAll(filepath.Dir(dir), 0o750); err != nil {
			return nil, fmt.Errorf("create index cache dir: %w", err)
		}
		if _, err := runGit(ctx, "clone", "--depth", "1", "--quiet", "--", indexURL, dir); err != nil {
			return nil, fmt.Errorf("fetch plugin index %s: %w", redactURL(indexURL), err)
		}
		touchIndexMarker(dir)
	}
	return loadPluginIndexFromDir(dir, indexURL)
}

// touchIndexMarker records a successful sync. Failing to write it only
// means the next run syncs again.
func touchIndexMarker(dir string) {
	if err := os.WriteFile(filepath.Join(dir, pluginIndexSyncMarker), nil, 0o600); err != nil {
		return
	}
}

func loadPluginIndexFromDir(dir, indexURL string) (*PluginIndex, error) {
	data, err := os.ReadFile(filepath.Join(dir, pluginIndexFileName)) //nolint:gosec // inside our cache dir
	if err != nil {
		return nil, fmt.Errorf("plugin index %s has no readable %s: %w", redactURL(indexURL), pluginIndexFileName, err)
	}
	if len(data) > maxPluginIndexSize {
		return nil, fmt.Errorf("%s is larger than %d bytes", pluginIndexFileName, maxPluginIndexSize)
	}
	return parsePluginIndex(data)
}

// parsePluginIndex decodes index.json, dropping entries with an invalid name,
// URL or description rather than failing the whole index.
func parsePluginIndex(data []byte) (*PluginIndex, error) {
	var idx PluginIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parse %s: %w", pluginIndexFileName, err)
	}
	if idx.Version != pluginIndexSchemaVersion {
		return nil, fmt.Errorf("%s has schema version %d; this mf understands %d (upgrade mf)",
			pluginIndexFileName, idx.Version, pluginIndexSchemaVersion)
	}
	valid := idx.Plugins[:0]
	for _, e := range idx.Plugins {
		if validatePluginName(e.Name) != nil || validatePluginRepoURL(e.RepoURL) != nil || hasControlChars(e.Description) {
			continue
		}
		valid = append(valid, e)
	}
	idx.Plugins = valid
	return &idx, nil
}

var errNotInIndex = errors.New("not in the plugin index")
