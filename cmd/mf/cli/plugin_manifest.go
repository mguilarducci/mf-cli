// Portions adapted from entireio/cli (https://github.com/entireio/cli),
// Copyright (c) 2026 Entire Inc., MIT License.
// See third_party/licenses/entireio-cli.LICENSE.

package cli

import (
	"fmt"

	"go.yaml.in/yaml/v3"

	"github.com/mguilarducci/mf-cli/cmd/mf/internal/registry"
)

const (
	pluginMetadataFileName = "mf-plugin.yml"
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
