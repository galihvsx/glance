package mail

import (
	"testing"

	"glance/internal/testdb"
)

// TestMain gives the mail package its own database (glance_test_mail) so
// its outbox rows never collide with parallel test packages. The database
// arrives freshly migrated; per-test migrate calls are unnecessary.
func TestMain(m *testing.M) {
	testdb.Main(m, "mail")
}
