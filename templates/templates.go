// Package templates embeds prompt templates that reviewers can edit without
// changing handler code.
package templates

import _ "embed"

// Suggest is the refinement prompt used by POST /v1/suggest.
//
//go:embed suggest.md
var Suggest string
