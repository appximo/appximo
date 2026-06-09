//go:build !dev

package main

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed web/dist
var embeddedUI embed.FS

func getUIHandler() http.Handler {
	dist, err := fs.Sub(embeddedUI, "web/dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	return http.FileServer(http.FS(dist))
}
