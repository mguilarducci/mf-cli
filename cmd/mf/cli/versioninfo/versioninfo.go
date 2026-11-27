// Package versioninfo holds build metadata, set by ldflags at release time
// (see .goreleaser.yaml).
package versioninfo

//nolint:gochecknoglobals // set via -ldflags -X
var (
	Version = "dev"
	Commit  = "none"
)
