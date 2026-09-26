package web

import (
	"embed"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"strings"
)

func init() {
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

//go:embed dist/*
var DistFS embed.FS

// Handler returns an http.Handler that serves the embedded SPA files with index.html fallback for client-side routing.
func Handler() http.Handler {
	subFS, err := fs.Sub(DistFS, "dist")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Dashboard web assets not available", http.StatusNotFound)
		})
	}
	fileServer := http.FileServer(http.FS(subFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		// If the file exists in the embedded asset FS, serve it directly (CSS, JS, SVG, etc.)
		f, err := subFS.Open(path)
		if err == nil {
			stat, statErr := f.Stat()
			f.Close()
			if statErr == nil && !stat.IsDir() {
				if path == "sw.js" {
					w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
					w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
					w.Header().Set("Service-Worker-Allowed", "/")
				} else if path == "manifest.webmanifest" {
					w.Header().Set("Content-Type", "application/manifest+json")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		// If path has a file extension (e.g. .js, .css, .ico, .png) and wasn't found, return 404
		if strings.Contains(path, ".") && !strings.HasSuffix(path, ".html") {
			http.NotFound(w, r)
			return
		}

		// Fallback to index.html for SPA client-side routes
		indexFile, err := subFS.Open("index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer indexFile.Close()

		stat, err := indexFile.Stat()
		if err != nil {
			http.NotFound(w, r)
			return
		}

		if seeker, ok := indexFile.(io.ReadSeeker); ok {
			http.ServeContent(w, r, "index.html", stat.ModTime(), seeker)
		} else {
			data, err := io.ReadAll(indexFile)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(data)
		}
	})
}
