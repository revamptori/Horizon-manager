// Package web embeds the production React bundle into the portable desktop executable.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:assets
var files embed.FS

func Assets() fs.FS { assets, _ := fs.Sub(files, "assets"); return assets }
