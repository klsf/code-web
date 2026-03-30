package main

import (
	"embed"
	"io/fs"
	"log"
)

//go:embed static
var embeddedAssets embed.FS

var staticFS fs.FS

func init() {
	sub, err := fs.Sub(embeddedAssets, "static")
	if err != nil {
		log.Fatalf("init embedded static fs: %v", err)
	}
	staticFS = sub
}
