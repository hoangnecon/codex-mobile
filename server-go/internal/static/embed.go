package static

import (
	"embed"
	"io/fs"
)

// EmbedDist holds the embedded frontend assets if built into the binary.
//
//go:embed all:dist
var EmbedDist embed.FS

// GetEmbeddedFS returns a sub-filesystem rooted at dist.
func GetEmbeddedFS() (fs.FS, error) {
	return fs.Sub(EmbedDist, "dist")
}
