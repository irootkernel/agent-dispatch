package config

import _ "embed"

// schemaJSON is a copy of docs/schemas/config.schema.json kept beside the
// loader so installed binaries validate against the same baseline.
// TestSchemaDrift fails verification when the copy diverges from the SOT
// package.
//
//go:embed schema/config.schema.json
var schemaJSON []byte
