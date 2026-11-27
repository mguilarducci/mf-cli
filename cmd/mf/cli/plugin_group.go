// Portions adapted from entireio/cli (https://github.com/entireio/cli),
// Copyright (c) 2026 Entire Inc., MIT License.
// See third_party/licenses/entireio-cli.LICENSE.

package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newPluginGroupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "plugin",
		Aliases: []string{"plugins"},
		Short:   "Manage mf plugins (install, list, remove)",
		Long: `A plugin is any executable named mf-<name>. Put it on PATH, or install it
with 'mf plugin install', and run it as 'mf <name>'.`,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newPluginInstallCmd(), newPluginListCmd(), newPluginRemoveCmd())
	return cmd
}

type installSourceKind int

const (
	installFromIndex installSourceKind = iota
	installFromURL
	installFromPath
)

type installSource struct {
	Kind installSourceKind
	Ref  string
}

// parseInstallSource classifies the install argument. A path must be explicit
// (a separator or leading .) so a stray local file never shadows an index name.
func parseInstallSource(arg string) (installSource, error) {
	if strings.HasPrefix(arg, "-") {
		return installSource{}, fmt.Errorf("install source %q must not start with '-'", arg)
	}
	switch {
	case strings.Contains(arg, "://") || scpLikeGitURL.MatchString(arg):
		if err := validatePluginRepoURL(arg); err != nil {
			return installSource{}, err
		}
		return installSource{Kind: installFromURL, Ref: arg}, nil
	case strings.ContainsAny(arg, `/\`) || strings.HasPrefix(arg, "."):
		return installSource{Kind: installFromPath, Ref: arg}, nil
	default:
		if err := validatePluginName(arg); err != nil {
			return installSource{}, err
		}
		return installSource{Kind: installFromIndex, Ref: arg}, nil
	}
}

type installFlags struct {
	name, pin, index            string
	force, yes, allowUnverified bool
}

func newPluginInstallCmd() *cobra.Command {
	var f installFlags
	cmd := &cobra.Command{
		Use:   "install <name|url|path>",
		Short: "Install a plugin from the index, a git repository URL, or a local executable",
		Long: `Install a plugin.

Three source forms:

  name   Bare names resolve through the plugin index:
             mf plugin install digisac
  url    Git repository URLs install from any forge. The newest stable semver
         tag is resolved with 'git ls-remote'; the platform's release asset is
         downloaded over HTTPS and verified against the release's
         checksums.txt. A release without checksums is refused unless
         --allow-unverified is passed:
             mf plugin install https://github.com/mguilarducci/mf-digisac
  path   Local executables are linked into the managed directory (a symlink,
         so rebuilds are picked up). Paths must be explicit, with a separator
         or leading ./, so a stray local file can never shadow an index name:
             mf plugin install ./bin/mf-digisac

Installing from a URL that is not in the plugin index asks for
confirmation; pass --yes to skip (required in non-interactive runs).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := parseInstallSource(args[0])
			if err != nil {
				return fmt.Errorf("install plugin: %w", err)
			}
			if src.Kind == installFromPath {
				name := f.name
				if name == "" {
					name = pluginNameFromPath(src.Ref)
				}
				dst, err := installLocalPlugin(src.Ref, name, f.force)
				if err != nil {
					return fmt.Errorf("install plugin: %w", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Installed plugin %q → %s\n", name, dst)
				return nil
			}
			return runRemoteInstall(cmd, src, f)
		},
	}
	cmd.Flags().StringVar(&f.name, "name", "", "Plugin name for a local path (default: file name without mf- prefix)")
	cmd.Flags().BoolVar(&f.force, "force", false, "Replace an existing plugin with the same name")
	cmd.Flags().BoolVar(&f.yes, "yes", false, "Skip the confirmation for sources outside the plugin index")
	cmd.Flags().StringVar(&f.pin, "pin", "", "Install exactly this tag")
	cmd.Flags().StringVar(&f.index, "index", "", "Plugin index URL (overrides "+pluginIndexEnvVar+" and the built-in default)")
	cmd.Flags().BoolVar(&f.allowUnverified, "allow-unverified", false,
		"Install even when the release publishes no "+checksumsFileName+" to authenticate the download")
	return cmd
}

func runRemoteInstall(cmd *cobra.Command, src installSource, f installFlags) error {
	ctx := cmd.Context()
	indexURL := resolvePluginIndexURL(f.index)
	repoURL, expectedName := src.Ref, ""

	if src.Kind == installFromIndex {
		idx, err := SyncPluginIndex(ctx, indexURL, false)
		if err != nil {
			return fmt.Errorf("install plugin: %w", err)
		}
		entry := idx.Find(src.Ref)
		if entry == nil {
			return fmt.Errorf("install plugin: %q is %w at %s; install by URL or path instead",
				src.Ref, errNotInIndex, redactURL(indexURL))
		}
		repoURL, expectedName = entry.RepoURL, entry.Name
	} else if !f.yes {
		// An unindexed URL is code from wherever the user typed: confirm.
		idx, err := SyncPluginIndex(ctx, indexURL, false)
		if err != nil || idx.FindByRepoURL(repoURL) == nil {
			ok, err := confirm(cmd, redactURL(repoURL)+" is not in the plugin index. Install and run code from it?")
			if err != nil {
				return fmt.Errorf("install plugin: %w", err)
			}
			if !ok {
				return errors.New("install plugin: cancelled")
			}
		}
	}

	m, err := InstallPluginFromRepo(ctx, repoURL, expectedName, RemoteInstallOptions{
		Pin: f.pin, Force: f.force, AllowUnverified: f.allowUnverified, Progress: cmd.ErrOrStderr(),
	})
	if err != nil {
		return fmt.Errorf("install plugin: %w", err)
	}
	note := ""
	if m.Unverified {
		note = " (unverified: no checksums)"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Installed plugin %q %s%s\nRun it with: mf %s\n", m.Name, m.Tag, note, m.Name)
	return nil
}

// confirm asks a yes/no question on the terminal. Non-interactive runs fail
// instead of guessing; --yes is the explicit opt-in.
func confirm(cmd *cobra.Command, prompt string) (bool, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return false, errors.New("confirmation needed but stdin is not a terminal; pass --yes")
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s [y/N] ", prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read answer: %w", err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func newPluginListCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List plugins found on PATH and in the managed dir",
		RunE: func(cmd *cobra.Command, _ []string) error {
			plugins := discoverPlugins()
			if output == "json" {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if plugins == nil {
					plugins = []pluginEntry{}
				}
				return enc.Encode(plugins)
			}
			if len(plugins) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "No plugins found. Install one with: mf plugin install <name|url|path>")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tVERSION\tSOURCE\tPATH")
			for _, p := range plugins {
				source, path, version := "PATH", p.Path, "-"
				if p.Managed {
					source = "local"
				}
				if p.Manifest != nil {
					source, version = redactURL(p.Manifest.RepoURL), p.Manifest.Tag
				} else if p.Target != "" {
					path = p.Path + " -> " + p.Target
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.Name, version, source, path)
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "table", "Output format: table or json")
	return cmd
}

func newPluginRemoveCmd() *cobra.Command {
	var purge bool
	cmd := &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm", "uninstall"},
		Short:   "Remove a plugin from the managed dir",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := removePlugin(args[0], purge); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Removed %q\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "Also delete the plugin's data dir")
	return cmd
}
