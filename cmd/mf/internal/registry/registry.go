// Package registry is the Plugin registry: where Managed plugins live and,
// in later steps, how a name resolves to a Plugin. It is built once from an
// explicit Env snapshot and never reads or changes the process environment.
package registry

import (
	"errors"
	"path/filepath"
	"strings"
)

// Env is the snapshot of the environment the registry is built from.
type Env struct {
	Path        []string // PATH entries, in order
	PluginDir   string   // MF_PLUGIN_DIR
	XDGDataHome string   // XDG_DATA_HOME
	Home        string   // home directory; empty when it can't be resolved
	PathExt     string   // PATHEXT (Windows)
	GOOS        string   // operating system, as runtime.GOOS
}

// Registry is the Plugin registry. Build it with New.
type Registry struct {
	env Env
	dir string
}

// New builds a registry from env. The directory is made absolute here, once,
// so every path the registry compares or links to is absolute; if it can't
// be, the registry is unavailable.
func New(env Env) *Registry {
	r := &Registry{env: env}
	switch {
	case env.PluginDir != "":
		r.dir = env.PluginDir
	case env.XDGDataHome != "":
		r.dir = filepath.Join(env.XDGDataHome, "mf", "plugins")
	case env.Home != "":
		r.dir = filepath.Join(env.Home, ".local", "share", "mf", "plugins")
	}
	if r.dir != "" && !r.isAbs(r.dir) {
		abs, err := filepath.Abs(r.dir)
		if err != nil {
			abs = ""
		}
		r.dir = abs
	}
	return r
}

// isAbs reports whether path is absolute by the snapshot's OS rules,
// whatever the host: on Windows a drive root (C:\, C:/) or a UNC path, else
// a leading slash.
func (r *Registry) isAbs(path string) bool {
	if !r.isWindows() {
		return strings.HasPrefix(path, "/")
	}
	if filepath.IsAbs(path) || strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, "//") {
		return true
	}
	if len(path) < 3 || path[1] != ':' || (path[2] != '\\' && path[2] != '/') {
		return false
	}
	drive := path[0] | 0x20 // lowercase ASCII letter
	return 'a' <= drive && drive <= 'z'
}

// ErrUnavailable is returned by operations that need the registry directory
// when it can't be resolved: no MF_PLUGIN_DIR, XDG_DATA_HOME or home directory.
var ErrUnavailable = errors.New("plugin registry directory unavailable: set MF_PLUGIN_DIR, XDG_DATA_HOME or HOME")

// Dir returns the registry directory.
func (r *Registry) Dir() (string, error) {
	if r.dir == "" {
		return "", ErrUnavailable
	}
	return r.dir, nil
}

// DataDir returns the per-plugin data directory. It is not created: that is
// the plugin's job on first use.
func (r *Registry) DataDir(name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	dir, err := r.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "data", name), nil
}
