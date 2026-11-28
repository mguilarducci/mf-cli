// Portions adapted from entireio/cli (https://github.com/entireio/cli),
// Copyright (c) 2026 Entire Inc., MIT License.
// See third_party/licenses/entireio-cli.LICENSE.

package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/mguilarducci/mf-cli/cmd/mf/internal/registry"
)

// Managed-install manifests. A plugin installed from a remote repository
// gets a per-plugin home under the managed dir:
//
//	<parent>/pkg/<name>/mf-<name>      the downloaded binary
//	<parent>/pkg/<name>/manifest.yml   how it was installed
//
// bin/mf-<name> links to the pkg binary, so the PATH dispatcher never changes.
// Local installs (`mf plugin install ./path`) have no manifest.

const (
	pluginMetadataFileName = "mf-plugin.yml"
	pluginManifestFileName = "manifest.yml"
	maxPluginMetadataSize  = 64 << 10
)

// PluginMetadata is the author-side mf-plugin.yml at the root of a plugin
// repository. Everything is optional: without it, the name comes from the
// repo name (mf-<name>) and assets from the forge's release downloads.
type PluginMetadata struct {
	Name        string `yaml:"name,omitempty"`
	Description string `yaml:"description,omitempty"`
	// DownloadURL overrides where release assets are fetched from. Placeholders:
	// {name} {tag} {version} {os} {arch} {asset}.
	DownloadURL string `yaml:"download_url,omitempty"`
}

// ParsePluginMetadata decodes mf-plugin.yml. Unknown keys are ignored so a
// field added later never breaks older mf binaries.
func ParsePluginMetadata(data []byte) (*PluginMetadata, error) {
	if len(data) > maxPluginMetadataSize {
		return nil, fmt.Errorf("%s is larger than %d bytes", pluginMetadataFileName, maxPluginMetadataSize)
	}
	var m PluginMetadata
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", pluginMetadataFileName, err)
	}
	if m.Name != "" {
		if err := registry.ValidateName(m.Name); err != nil {
			return nil, fmt.Errorf("%s: %w", pluginMetadataFileName, err)
		}
	}
	if hasControlChars(m.Description) || hasControlChars(m.DownloadURL) {
		return nil, fmt.Errorf("%s must not contain control characters", pluginMetadataFileName)
	}
	return &m, nil
}

// PluginManifest records how a managed plugin was installed.
type PluginManifest struct {
	Name        string    `json:"name"                  yaml:"name"`
	RepoURL     string    `json:"repo_url"              yaml:"repo_url"`
	Tag         string    `json:"tag"                   yaml:"tag"`
	Asset       string    `json:"asset,omitempty"       yaml:"asset,omitempty"`
	SHA256      string    `json:"sha256,omitempty"      yaml:"sha256,omitempty"`
	Unverified  bool      `json:"unverified,omitempty"  yaml:"unverified,omitempty"`
	Pinned      bool      `json:"pinned,omitempty"      yaml:"pinned,omitempty"`
	InstalledAt time.Time `json:"installed_at"          yaml:"installed_at"`
	Description string    `json:"description,omitempty" yaml:"description,omitempty"`
}

// PluginPkgDir returns the per-plugin package directory. Not created.
func PluginPkgDir(name string) (string, error) {
	if err := registry.ValidateName(name); err != nil {
		return "", err
	}
	parent, err := pluginParentDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, "pkg", name), nil
}

// LoadPluginManifest returns (nil, nil) when the plugin has no manifest:
// local installs and raw-PATH plugins.
func LoadPluginManifest(name string) (*PluginManifest, error) {
	dir, err := PluginPkgDir(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, pluginManifestFileName)) //nolint:gosec // inside the managed pkg dir
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil //nolint:nilnil // absence is the common case
	}
	if err != nil {
		return nil, fmt.Errorf("read plugin manifest: %w", err)
	}
	var m PluginManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse plugin manifest: %w", err)
	}
	return &m, nil
}

// SavePluginManifest writes the manifest into the plugin's pkg dir.
func SavePluginManifest(m *PluginManifest) error {
	dir, err := PluginPkgDir(m.Name)
	if err != nil {
		return err
	}
	data, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("encode plugin manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, pluginManifestFileName), data, 0o600); err != nil {
		return fmt.Errorf("write plugin manifest: %w", err)
	}
	return nil
}
