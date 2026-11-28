package registry

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Plugin is one mf-<name> executable found on the Search path.
type Plugin struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Target  string `json:"target,omitempty"` // symlink target, if any
	Managed bool   `json:"managed"`
	// Manifest is set for Managed plugins installed from a repository.
	Manifest *Manifest `json:"manifest,omitempty"`
}

// binDir is where Managed plugins are placed; empty when unavailable.
func (r *Registry) binDir() string {
	if r.dir == "" {
		return ""
	}
	return filepath.Join(r.dir, "bin")
}

// searchDirs is the Search path as scanned: the registry's bin dir, then
// each absolute PATH entry. Empty and relative entries (".", "bin") are
// skipped, so a binary planted under the working directory never runs.
// Go's ErrDot check alone would miss a relative entry with a separator.
func (r *Registry) searchDirs() []string {
	var dirs []string
	if bin := r.binDir(); bin != "" {
		dirs = append(dirs, bin)
	}
	for _, dir := range r.env.Path {
		if filepath.IsAbs(dir) {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// Resolve returns the Plugin that `mf <name>` runs: the first match on the
// Search path.
func (r *Registry) Resolve(name string) (Plugin, bool) {
	if ValidateName(name) != nil {
		return Plugin{}, false
	}
	for _, dir := range r.searchDirs() {
		if path, ok := r.findIn(dir, name); ok {
			return r.plugin(name, dir, path), true
		}
	}
	return Plugin{}, false
}

// List returns every Plugin on the Search path, sorted by name. Each name
// appears once, as the Plugin Resolve returns for it.
func (r *Registry) List() []Plugin {
	seen := map[string]bool{}
	var out []Plugin
	for _, dir := range r.searchDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name, ok := strings.CutPrefix(e.Name(), binaryPrefix)
			if !ok {
				continue
			}
			if r.isWindows() {
				name = strings.TrimSuffix(name, filepath.Ext(name))
			}
			if seen[name] || ValidateName(name) != nil {
				continue
			}
			if path, ok := r.findIn(dir, name); ok {
				seen[name] = true
				out = append(out, r.plugin(name, dir, path))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// findIn looks for the plugin name in one directory. On Unix that is an
// executable regular file mf-<name>. On Windows it is mf-<name><ext>, trying
// each PATHEXT extension in order, as exec.LookPath does.
func (r *Registry) findIn(dir, name string) (string, bool) {
	base := filepath.Join(dir, binaryPrefix+name)
	candidates := []string{base}
	if r.isWindows() {
		candidates = nil
		for _, ext := range r.pathExts() {
			candidates = append(candidates, base+ext)
		}
	}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && r.isExecutable(path, info) {
			return path, true
		}
	}
	return "", false
}

// pathExts is PATHEXT normalized like exec.LookPath: lower case, each with a
// leading dot, defaulting to .com;.exe;.bat;.cmd when empty.
func (r *Registry) pathExts() []string {
	var exts []string
	for _, e := range strings.Split(strings.ToLower(r.env.PathExt), ";") {
		if e == "" {
			continue
		}
		if e[0] != '.' {
			e = "." + e
		}
		exts = append(exts, e)
	}
	if len(exts) == 0 {
		return []string{".com", ".exe", ".bat", ".cmd"}
	}
	return exts
}

func (r *Registry) plugin(name, dir, path string) Plugin {
	target, err := os.Readlink(path)
	if err != nil {
		target = "" // not a symlink
	}
	p := Plugin{Name: name, Path: path, Target: target, Managed: dir == r.binDir()}
	if p.Managed {
		// An unreadable manifest still leaves a runnable plugin.
		if m, err := r.loadManifest(name); err == nil {
			p.Manifest = m
		}
	}
	return p
}

// SearchPath is the Search path as a PATH value, for a launched plugin's
// environment: the registry's bin dir first, then PATH exactly as given.
func (r *Registry) SearchPath() string {
	dirs := r.env.Path
	if bin := r.binDir(); bin != "" {
		dirs = append([]string{bin}, dirs...)
	}
	sep := ":"
	if r.isWindows() {
		sep = ";"
	}
	return strings.Join(dirs, sep)
}
