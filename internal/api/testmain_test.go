package api

import (
	"testing"

	"glance/internal/testdb"
)

// TestMain gives the api package its own database (glance_test_api) so its
// rows never collide with parallel test packages. The database arrives
// freshly migrated — including an empty rate_limits table, so the old
// cross-run TRUNCATE (C6T6) is dead code; within-run isolation stays on
// uniqueIP()/uniqueEmail().
func TestMain(m *testing.M) {
	testdb.Main(m, "api")
}
