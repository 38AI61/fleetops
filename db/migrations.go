// Package db embeds the SQL migrations. Embedding does not apply them;
// migrations run only through `make migrate`.
package db

import "embed"

//go:embed migrations/*.sql
var Migrations embed.FS
