package perf

import (
	"testing"

	"glance/internal/testdb"
)

// TestMain gives the perf package its own database (glance_test_perf) so
// its 500-row seed corpus never collides with parallel test packages.
// The database arrives freshly migrated; per-test migrate calls are
// unnecessary.
func TestMain(m *testing.M) {
	testdb.Main(m, "perf")
}
