// Package migrations embeds MIA's forward-only SQLite migrations.
package migrations

import "embed"

// Files contains every forward-only database migration shipped by this binary.
//
//go:embed *.up.sql
var Files embed.FS
