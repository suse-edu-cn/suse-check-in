package webassets

import "embed"

//go:embed index.html static
var FS embed.FS
