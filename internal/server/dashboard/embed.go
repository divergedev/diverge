// Package dashboard provides embedded static assets for the Diverge web dashboard.
package dashboard

import "embed"

// Assets contains the compiled frontend dashboard files.
//
//go:embed all:dist
var Assets embed.FS
