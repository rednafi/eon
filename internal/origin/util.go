package origin

import "strings"

// toLower is strings.ToLower; aliased so the file imports stay tidy in tests.
func toLower(s string) string { return strings.ToLower(s) }

// containsFold reports whether sub is in s, case-insensitively.
func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), sub)
}
