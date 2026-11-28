// Portions adapted from entireio/cli (https://github.com/entireio/cli),
// Copyright (c) 2026 Entire Inc., MIT License.
// See third_party/licenses/entireio-cli.LICENSE.

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mguilarducci/mf-cli/cmd/mf/internal/registry"
)

// maxTagFallbacks bounds how many older tags are tried when the newest has no
// asset for this platform (a release still uploading, or a dropped target).
const maxTagFallbacks = 3

type RemoteInstallOptions struct {
	Pin             string
	Force           bool
	AllowUnverified bool
	Progress        io.Writer
}

// InstallPluginFromRepo installs the newest stable release of repoURL (or
// opts.Pin). expectedName, when set, must match the plugin's declared name.
func InstallPluginFromRepo(ctx context.Context, reg *registry.Registry, repoURL, expectedName string, opts RemoteInstallOptions) (*PluginManifest, error) {
	repoURL = strings.TrimRight(repoURL, "/")
	tags := []string{opts.Pin}
	if opts.Pin == "" {
		step(opts.Progress, "Finding latest release of %s", redactURL(repoURL))
		var err error
		tags, err = listRemoteSemverTags(ctx, repoURL)
		if err != nil {
			return nil, err
		}
		if len(tags) == 0 {
			return nil, fmt.Errorf("%s has no stable semver tags; pass --pin <tag> to install a prerelease or non-semver tag", redactURL(repoURL))
		}
		if len(tags) > maxTagFallbacks {
			tags = tags[:maxTagFallbacks]
		}
	}
	var lastErr error
	for _, tag := range tags {
		m, err := installRepoAtTag(ctx, reg, repoURL, expectedName, tag, opts)
		if err == nil {
			return m, nil
		}
		lastErr = err
		if !errors.Is(err, errAssetNotFound) {
			return nil, err
		}
	}
	return nil, lastErr
}

func installRepoAtTag(ctx context.Context, reg *registry.Registry, repoURL, expectedName, tag string, opts RemoteInstallOptions) (*PluginManifest, error) {
	step(opts.Progress, "Reading %s at %s", pluginMetadataFileName, tag)
	meta, err := fetchPluginMetadataAtTag(ctx, repoURL, tag)
	if err != nil {
		return nil, err
	}
	name, err := remotePluginName(meta, repoURL, expectedName)
	if err != nil {
		return nil, err
	}
	if err := reg.CanInstall(name, opts.Force); err != nil {
		return nil, fmt.Errorf("check plugin %q: %w", name, err)
	}

	staging, err := os.MkdirTemp("", "mf-plugin-fetch-")
	if err != nil {
		return nil, fmt.Errorf("create staging dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()

	step(opts.Progress, "Downloading %s%s %s", registry.BinaryPrefix, name, tag)
	asset, err := downloadPluginAsset(ctx, meta, repoURL, name, tag, staging, opts.AllowUnverified)
	if err != nil {
		return nil, err
	}
	staged := filepath.Join(staging, "extracted-"+pluginBinaryName(name))
	if err := extractPluginBinary(asset.Path, name, staged); err != nil {
		return nil, err
	}

	manifest := &PluginManifest{
		Name:        name,
		RepoURL:     repoURL,
		Tag:         tag,
		Asset:       asset.Asset,
		SHA256:      asset.SHA256,
		Unverified:  !asset.Verified,
		Pinned:      opts.Pin != "",
		InstalledAt: time.Now().UTC(),
	}
	if meta != nil {
		manifest.Description = meta.Description
	}
	if err := placeStagedBinary(reg, staged, manifest, opts.Force); err != nil {
		return nil, err
	}
	return manifest, nil
}

func remotePluginName(meta *PluginMetadata, repoURL, expectedName string) (string, error) {
	name := ""
	if meta != nil {
		name = meta.Name
	}
	if name == "" {
		var err error
		if name, err = pluginNameFromRepoURL(repoURL); err != nil {
			return "", err
		}
	}
	if expectedName != "" && name != expectedName {
		return "", fmt.Errorf("%s declares plugin name %q but %q was requested; refusing to install under a name that was not asked for",
			redactURL(repoURL), name, expectedName)
	}
	return name, nil
}

// placeStagedBinary moves the binary into pkg/<name>/, writes the manifest,
// and points bin/mf-<name> at it. With force, the old Managed plugin is
// removed (data kept) only now, once the new binary is downloaded.
func placeStagedBinary(reg *registry.Registry, staged string, m *PluginManifest, force bool) error {
	if force {
		if err := reg.Remove(m.Name, false); err != nil && !errors.Is(err, registry.ErrNotManaged) {
			return fmt.Errorf("replace plugin %q: %w", m.Name, err)
		}
	}
	pkgDir, err := PluginPkgDir(m.Name)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(pkgDir); err != nil {
		return fmt.Errorf("clear previous install: %w", err)
	}
	if err := os.MkdirAll(pkgDir, 0o750); err != nil {
		return fmt.Errorf("create plugin pkg dir: %w", err)
	}
	pkgBin := filepath.Join(pkgDir, pluginBinaryName(m.Name))
	if err := os.Rename(staged, pkgBin); err != nil {
		// Staging may be on another filesystem (TMPDIR); fall back to a copy.
		if err := copyExecutable(staged, pkgBin); err != nil {
			return err
		}
	}
	if err := SavePluginManifest(m); err != nil {
		return err
	}
	if _, err := reg.InstallLocal(pkgBin, m.Name, false); err != nil {
		return fmt.Errorf("link plugin %q: %w", m.Name, err)
	}
	return nil
}

// pluginBinaryName is the on-disk executable name for a plugin.
func pluginBinaryName(name string) string {
	if isWindows() {
		return registry.BinaryPrefix + name + ".exe"
	}
	return registry.BinaryPrefix + name
}

func step(w io.Writer, format string, args ...any) {
	if w != nil {
		_, _ = fmt.Fprintf(w, "==> "+format+"\n", args...)
	}
}
