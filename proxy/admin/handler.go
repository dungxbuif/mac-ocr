package admin

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed static/*
var staticFS embed.FS

func Handler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if _, err := fs.Stat(sub, path); err != nil {
				clone := r.Clone(r.Context())
				urlCopy := *r.URL
				urlCopy.Path = "/"
				clone.URL = &urlCopy
				files.ServeHTTP(w, clone)
				return
			}
		}
		files.ServeHTTP(w, r)
	})
}
