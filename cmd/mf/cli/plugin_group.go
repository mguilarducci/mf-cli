// Portions adapted from entireio/cli (https://github.com/entireio/cli),
// Copyright (c) 2026 Entire Inc., MIT License.
// See third_party/licenses/entireio-cli.LICENSE.

package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/mguilarducci/mf-cli/cmd/mf/internal/registry"
)

func newPluginGroupCmd(_ *registry.Registry) *cobra.Command {
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
	installFromRepo installSourceKind = iota // name or owner/name, resolved by convention
	installFromURL
	installFromPath
)

// Plugin repositories are found by convention, not through an index:
// owner/name -> https://github.com/owner/mf-name, and a bare name uses the
// default owner.
const (
	defaultPluginOwner = "mguilarducci"
	pluginForgeURL     = "https://github.com"
)

var ownerNameRe = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9-]*)/([A-Za-z0-9._-]+)$`)

type installSource struct {
	Kind     installSourceKind
	Ref      string // URL for repo/URL sources, file path for path sources
	Name     string // plugin name expected from a name or owner/name source
	Bare     bool   // a bare name: the default owner is a guess, not the user's choice
	Answered bool   // typed in answer to the not-found prompt
}

// parseInstallSource classifies the install argument:
//
//	digisac            -> https://github.com/mguilarducci/mf-digisac
//	owner/name         -> https://github.com/owner/mf-name (mf- prefix optional)
//	https://…, git@…   -> that repository
//	./x, ../x, /x      -> a local executable
//
// owner/name and a relative path look alike, so paths must start with ./,
// ../ or /, and an owner/name that also exists as a local file is refused
// rather than guessed.
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
	case strings.HasPrefix(arg, "./") || strings.HasPrefix(arg, "../") || filepath.IsAbs(arg) ||
		arg == "." || strings.HasPrefix(arg, `.\`) || strings.HasPrefix(arg, `..\`):
		return installSource{Kind: installFromPath, Ref: arg}, nil
	case ownerNameRe.MatchString(arg):
		if _, err := os.Stat(arg); err == nil {
			return installSource{}, fmt.Errorf("%q is both a local file and an owner/name; use ./%s for the file or the full repository URL", arg, arg)
		}
		m := ownerNameRe.FindStringSubmatch(arg)
		name := strings.TrimPrefix(m[2], pluginBinaryPrefix)
		if err := registry.ValidateName(name); err != nil {
			return installSource{}, err
		}
		return installSource{Kind: installFromRepo, Ref: conventionRepoURL(m[1], name), Name: name}, nil
	case strings.ContainsAny(arg, `/\`):
		return installSource{}, fmt.Errorf("%q is neither owner/name nor an explicit path; local paths start with ./, ../ or /", arg)
	default:
		if err := registry.ValidateName(arg); err != nil {
			return installSource{}, err
		}
		return installSource{Kind: installFromRepo, Ref: conventionRepoURL(defaultPluginOwner, arg), Name: arg, Bare: true}, nil
	}
}

func conventionRepoURL(owner, name string) string {
	return pluginForgeURL + "/" + owner + "/" + pluginBinaryPrefix + name
}

// trustedRepo reports whether repoURL belongs to the default owner, whose
// plugins install without a confirmation prompt.
func trustedRepo(repoURL string) bool {
	return strings.HasPrefix(normalizeRepoURL(repoURL)+"/", pluginForgeURL+"/"+defaultPluginOwner+"/")
}

func normalizeRepoURL(u string) string {
	return strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(u), "/"), ".git")
}

type installFlags struct {
	name, pin                   string
	force, yes, allowUnverified bool
}

func newPluginInstallCmd() *cobra.Command {
	var f installFlags
	cmd := &cobra.Command{
		Use:   "install <name|owner/name|url|path>",
		Short: "Install a plugin from a git repository or a local executable",
		Long: `Install a plugin.

Source forms:

  name        Looked up under the default owner first; if there is no such
              repository, mf asks for owner/repo:
                  mf plugin install digisac      # github.com/` + defaultPluginOwner + `/mf-digisac
  owner/name  A plugin repository named mf-<name> on GitHub:
                  mf plugin install someone/foo  # github.com/someone/mf-foo
  url         Any git repository URL:
                  mf plugin install https://github.com/` + defaultPluginOwner + `/mf-digisac
  path        A local executable, linked into the managed directory (a
              symlink, so rebuilds are picked up). Paths start with ./, ../
              or /, so they can't be mistaken for owner/name:
                  mf plugin install ./bin/mf-digisac

For a repository, the newest stable semver tag is resolved with
'git ls-remote', and the platform's release asset is downloaded over HTTPS
and verified against the release's checksums.txt. A release without
checksums is refused unless --allow-unverified is passed.

Repositories outside ` + defaultPluginOwner + ` ask for confirmation first; pass --yes to
skip (required in non-interactive runs).`,
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
	cmd.Flags().BoolVar(&f.yes, "yes", false, "Skip the confirmation for repositories outside "+defaultPluginOwner)
	cmd.Flags().StringVar(&f.pin, "pin", "", "Install exactly this tag")
	cmd.Flags().BoolVar(&f.allowUnverified, "allow-unverified", false,
		"Install even when the release publishes no "+checksumsFileName+" to authenticate the download")
	return cmd
}

func runRemoteInstall(cmd *cobra.Command, src installSource, f installFlags) error {
	ctx := cmd.Context()
	if src.Bare {
		resolved, err := resolveBareName(ctx, cmd, src, os.Stdin, stdinIsTerminal())
		if err != nil {
			return fmt.Errorf("install plugin: %w", err)
		}
		src = resolved
	}
	repoURL, expectedName := src.Ref, src.Name

	// A bare name that fell back to a typed owner/repo was the user's explicit
	// answer, so it is not asked about twice.
	if !f.yes && !src.Answered && !trustedRepo(repoURL) {
		// Code from wherever the user pointed: confirm before running it.
		ok, err := confirm(cmd, "Install and run plugin code from "+redactURL(repoURL)+"?")
		if err != nil {
			return fmt.Errorf("install plugin: %w", err)
		}
		if !ok {
			return errors.New("install plugin: cancelled")
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

// repoExists is a seam for tests: it reports whether a git remote answers.
//
//nolint:gochecknoglobals // test seam
var repoExists = func(ctx context.Context, repoURL string) bool {
	_, err := runGit(ctx, "ls-remote", "--heads", "--", repoURL)
	return err == nil
}

func stdinIsTerminal() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// resolveBareName looks a bare name up under the default owner first. When
// that repository does not exist, it asks for owner/repo instead (a
// non-interactive run fails and says to pass one).
func resolveBareName(ctx context.Context, cmd *cobra.Command, src installSource, in io.Reader, interactive bool) (installSource, error) {
	if repoExists(ctx, src.Ref) {
		return src, nil
	}
	notFound := fmt.Sprintf("no plugin %q at %s", src.Name, redactURL(src.Ref))
	if !interactive {
		return installSource{}, fmt.Errorf("%s; pass owner/repo, e.g. mf plugin install someone/%s", notFound, src.Name)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s.\nowner/repo (empty to cancel): ", notFound)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return installSource{}, fmt.Errorf("read answer: %w", err)
	}
	answer := strings.TrimSpace(line)
	if answer == "" {
		return installSource{}, errors.New("cancelled")
	}
	next, err := parseInstallSource(answer)
	if err != nil {
		return installSource{}, err
	}
	if next.Kind == installFromPath || next.Bare {
		return installSource{}, fmt.Errorf("%q is not owner/repo or a repository URL", answer)
	}
	next.Answered = true
	return next, nil
}

// confirm asks a yes/no question on the terminal. Non-interactive runs fail
// instead of guessing; --yes is the explicit opt-in.
func confirm(cmd *cobra.Command, prompt string) (bool, error) {
	if !stdinIsTerminal() {
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
				fmt.Fprintln(cmd.ErrOrStderr(), "No plugins found. Install one with: mf plugin install <name|owner/name|url|path>")
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
