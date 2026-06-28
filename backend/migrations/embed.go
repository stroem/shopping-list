// Package migrations embeds the SQL migration files so they travel with the
// compiled binary (including the Lambda artifact) — no runtime file path needed.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
