# Changelog

All notable changes to this project are documented here. The format is based
on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

The release workflow publishes the section matching the pushed tag as the
GitHub release notes, so add one before running `mise run release vX.Y.Z`.

## [Unreleased]

## [0.1.0]

### Added

- `mf <name>` runs an `mf-<name>` plugin from PATH, kubectl-style.
- `mf plugin install|list|remove` to manage plugins in `~/.local/share/mf/plugins`.
- Plugins listed under "Plugin Commands" in `mf --help`.
