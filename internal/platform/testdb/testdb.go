// Package testdb picks the database for integration tests.
//
// Integration tests insert packs, spots, devices and rules and do not clean
// all of it up, so they must never run against the main database (`picnic`,
// the copy that seeds production). They use TEST_DATABASE_URL — locally the
// scratch database `picnic_test`. DATABASE_URL is accepted only on CI, where
// the database is a throwaway service container.
package testdb

import (
	"os"
	"testing"
)

// DSN returns the integration-test database URL, or skips the test when
// none is configured.
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
