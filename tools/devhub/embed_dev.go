//go:build dev

package main

import "net/http"

func getUIHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "dev mode — UI en http://localhost:5173", http.StatusNotFound)
	})
}
