//go:build unix && !aix

package registry

import "golang.org/x/sys/unix"

// hostCanExecute reports whether the current user may execute path, checked
// against the effective IDs as exec.LookPath does, so an execute bit that is
// only the group's or other users' doesn't count.
func hostCanExecute(path string) bool {
	return unix.Faccessat(unix.AT_FDCWD, path, unix.X_OK, unix.AT_EACCESS) == nil
}
