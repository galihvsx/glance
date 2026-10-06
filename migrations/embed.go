// Package migrations embeds the SQL migration files so the store
// migration runner can apply them from the compiled binary.
package migrations

import "embed"

// FS holds the migration files (*.up.sql and *.down.sql), rooted at
// this directory. Pass it (or an fs.Sub of it) to store.Migrate —
// the runner only applies *.up.sql, in filename order.
var (
	//go:embed *.sql
	FS embed.FS
)
