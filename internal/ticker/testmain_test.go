package ticker

import (
	"testing"

	"glance/internal/testdb"
)

// TestMain gives the ticker package its own database (glance_test_ticker)
// so its rows never collide with parallel test packages. The database
// arrives freshly migrated; per-test migrate calls are unnecessary.
func TestMain(m *testing.M) {
	testdb.Main(m, "ticker")
}
