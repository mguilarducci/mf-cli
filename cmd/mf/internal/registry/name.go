package registry

import (
	"fmt"
	"regexp"
)

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// ValidateName rejects names the dispatcher would never invoke: empty,
// flag-shaped, path-like, or with uppercase or other characters.
func ValidateName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid plugin name %q: use lowercase letters, digits, '-' or '_'", name)
	}
	return nil
}
