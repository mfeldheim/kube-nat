// Package webui provides the embedded React SPA assets for the kube-nat dashboard.
// Build the SPA first with `make build-web`, then `go build ./...` will embed the output.
package webui

import "embed"

//go:embed dist
var FS embed.FS
