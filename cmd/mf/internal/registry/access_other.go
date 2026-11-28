//go:build !unix || aix

package registry

// hostCanExecute has no access check to make on this host: Windows goes by
// PATHEXT, and the mode bits decide everywhere else.
func hostCanExecute(string) bool { return true }
