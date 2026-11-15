package testdb

import (
	"os"
	"testing"
)

func DSN(t testing.TB) string {
	t.Helper()
	if dsn := os.Getenv("TEST_DATABASE_URL"); dsn != "" {
		return dsn
	}
	if os.Getenv("CI") != "" {
		if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
			return dsn
		}
	}
	t.Skip("TEST_DATABASE_URL not set (integration tests write data; DATABASE_URL is only used on CI)")
	return ""
}
