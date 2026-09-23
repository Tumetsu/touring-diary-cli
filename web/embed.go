// Package web holds the static frontend (spec section 6), embedded into the
// binary and copied into the output folder by the build.
package web

import "embed"

// FS contains index.html, app.js, style.css and the vendored libraries.
//
//go:embed index.html app.js style.css vendor
var FS embed.FS
