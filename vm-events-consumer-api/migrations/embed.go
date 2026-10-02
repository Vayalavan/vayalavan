// Package migrations embeds vm-events-consumer-api's goose migrations for the
// analytics schema, so the schema that runs always matches the deployed code.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS

// Dir is the path to the migrations within FS. They sit at its root.
const Dir = "."
