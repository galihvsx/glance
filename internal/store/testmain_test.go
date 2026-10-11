// Package store_test holds the TestMain in the external test package: the
// internal test files live in package store, and internal/testdb imports
// glance/internal/store, so an internal TestMain would create an import
// cycle. A TestMain in the external test package still governs the whole
// test binary.
package store_test

import (
	"testing"

	"glance/internal/testdb"
)

// TestMain gives the store package its own database (glance_test_store) so
// its migration-runner tests never collide with parallel test packages.
// The database arrives freshly migrated; the migration tests below apply
// their own self-contained testMigrations set on top.
func TestMain(m *testing.M) {
	testdb.Main(m, "store")
}
