// Package identifier validates role and capability identifiers shared by
// local assignments and Core desired state.
package identifier

import "regexp"

var pattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Valid reports whether value uses lowercase letters, digits, and internal
// hyphens, with no leading, trailing, or repeated hyphen.
func Valid(value string) bool {
	return pattern.MatchString(value)
}
