package web

import "embed"

// assetFS holds the browser assets. The budget test in assets_test.go caps its size.
//
//go:embed assets
var assetFS embed.FS

const indexPath = "assets/index.html"
