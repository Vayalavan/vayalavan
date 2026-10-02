// Package migrations embeds vm-orders-api's goose migration files into the
// binary, so the migrations that run always match the code that was deployed.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS

// Dir is the path to the migrations within FS. They sit at its root.
const Dir = "."
