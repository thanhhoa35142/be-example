package ingest

import "strings"

func normalizeName(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}
