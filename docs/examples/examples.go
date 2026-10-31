// Package examples embeds the example pipeline topologies, so the browser
// editor can start a draft from one. The JSON files stay the documentation.
package examples

import "embed"

// Pipelines holds the pipeline-*.json example topologies.
//
//go:embed pipeline-*.json
var Pipelines embed.FS
