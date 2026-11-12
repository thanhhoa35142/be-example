package ingest

import "strings"

// normalizeName canonicalizes a spot/candidate name for fallback matching:
// lowercase, trimmed, internal whitespace runs collapsed to one space.
func normalizeName(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}
