package registry

import (
	"os"
	"path/filepath"
	"runtime"
)

// EnvFromProcess snapshots the real process environment. A home directory
// that can't be resolved is left empty, which New treats as unavailable
// unless MF_PLUGIN_DIR or XDG_DATA_HOME is set.
func EnvFromProcess() Env {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return Env{
		Path:        filepath.SplitList(os.Getenv("PATH")),
		PluginDir:   os.Getenv("MF_PLUGIN_DIR"),
		XDGDataHome: os.Getenv("XDG_DATA_HOME"),
		Home:        home,
		PathExt:     os.Getenv("PATHEXT"),
		GOOS:        runtime.GOOS,
	}
}
