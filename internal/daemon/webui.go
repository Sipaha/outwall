package daemon

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:webdist
var webdistFS embed.FS

// staticUI serves the embedded SPA: existing files are served directly, unknown
// paths fall back to index.html (client-side routing).
func staticUI() http.Handler {
	sub, err := fs.Sub(webdistFS, "webdist")
	if err != nil {
		// webdist is a compile-time embed; fs.Sub on a constant valid path cannot fail.
		// Serve a 500 rather than panic, to honor the no-panic-in-library rule.
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "ui assets unavailable", http.StatusInternalServerError)
		})
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/" {
			files.ServeHTTP(w, r)
			return
		}
		if _, err := fs.Stat(sub, p[1:]); err != nil {
			http.ServeFileFS(w, r, sub, "index.html") // SPA fallback
			return
		}
		files.ServeHTTP(w, r)
	})
}
