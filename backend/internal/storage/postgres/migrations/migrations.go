package migrations

import "embed"

// Files contains all schema migrations.
//
//go:embed sql/*.sql
var Files embed.FS
