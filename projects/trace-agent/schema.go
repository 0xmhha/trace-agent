// Package contract embeds the canonical trace database definition.
package contract

import _ "embed"

// Schema initializes an empty v1.3 database.
//
//go:embed schema.sql
var Schema string
