# AGENTS.md

## Commands

Tooling is pinned in `mise.toml`. Keep the Go version there aligned with `go.mod`.

- `mise run build`: build `bin/mf`
- `mise run test`: run all tests through gotestsum
- `mise run test:ci`: run `go test -race ./...`
- `mise run lint`: golangci-lint, gofmt check, `go mod tidy` check, shellcheck. It is read-only; `mise run fmt` does the rewriting.
- `mise run lint:vuln`: govulncheck. This is not part of `lint`.
- `mise run check`: fmt + lint + test:ci
- Single test: `go test ./cmd/mf/cli -run TestPluginEnv`
- `mise run release:snapshot`: build release archives locally with goreleaser

Releases: `mise run release vX.Y.Z` (needs a clean tree) tags and pushes; the release workflow runs goreleaser, and GitHub generates the release notes from the merged PRs since the previous tag. Version and commit are injected through ldflags into `cmd/mf/cli/versioninfo`.

## Architecture

`mf` is a kubectl-style plugin aggregator. The CLI is in `cmd/mf/cli` (Cobra). The **Plugin registry** is `cmd/mf/internal/registry` (no Cobra). It is built once in `main.go` from an explicit environment snapshot and passed to the root command and the dispatcher.

- **Dispatch happens before Cobra.** `main.go` calls `MaybeRunPlugin`. For `mf <name>`, if `<name>` is not a built-in command (`help` and `completion` included), the registry resolves `mf-<name>` on its **Search path** (the registry's `bin` dir, then each absolute `PATH` entry; first match wins) and it runs with stdio and exit code passed through, with the Search path as its `PATH`. `mf plugin list` and root help use the same registry list. `mf` never changes its own environment. Plugins are not Cobra commands; they only appear in root help (`plugin_help.go`).
- **Plugin environment is an allowlist** (`plugin_env.go`). A plugin doesn't get the full parent env: it gets a fixed allowlist, its own `<NAME>_*` vars (for example `DIGISAC_*` for `mf-digisac`), anything listed in `MF_PLUGIN_ENV`, plus `MF_CLI_VERSION`, `MF_PLUGIN_NAME` and `MF_PLUGIN_DATA_DIR`.
- **Plugin registry layout** (`registry.go`; installs in `install.go` and `staged.go`, `manifest.go`), under `MF_PLUGIN_DIR`, else `$XDG_DATA_HOME/mf/plugins`, else `~/.local/share/mf/plugins`:
  - `bin/mf-<name>`: the dispatch surface (a symlink, or a copy as fallback)
  - `pkg/<name>/`: repository installs, holding the binary and `manifest.yml`. Local-path installs have no manifest.
  - `data/<name>/`: per-plugin storage. The plugin creates it.
- **`mf plugin install`** (`plugin_group.go`, `plugin_install_remote.go`, `plugin_gitremote.go`, `plugin_fetch.go`) has no index. A source is resolved by convention: `name` → `github.com/mguilarducci/mf-name`, `owner/name` → `github.com/owner/mf-name`. A source can also be a URL or a local path. An optional `mf-plugin.yml` at the repo root overrides the name and asset download template. Release assets are matched per OS/arch, verified against a sha256 checksums file (refused without one unless explicitly allowed), and must use HTTPS (loopback is allowed for tests).

## Conventions

- Much of the plugin code, plus the mise, lint, goreleaser and CI config, is adapted from entireio/cli (MIT). Keep the attribution header on those files, and update `NOTICE` when adapting more code.
- Tests build the registry from an explicit `registry.Env` (a temp `PluginDir`, their own `Path` and `GOOS`) rather than the process environment, and use loopback HTTP servers for remote installs. Windows rules come from the snapshot's `GOOS`, so they are tested on every OS.

## Agent skills

### Issue tracker

Issues are tracked in GitHub Issues for `mguilarducci/mf-cli` (via `gh`). See `docs/agents/issue-tracker.md`.

### Triage labels

Default canonical labels: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `GLOSSARY.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.
