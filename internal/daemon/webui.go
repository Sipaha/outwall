package daemon

import (
	"io/fs"
	"net/http"
	"strings"
)

// uiFS / uiRoot are provided by exactly one of webui_dev.go (dev, embeds the tracked webseed/
// placeholder) or webui_prod.go (built with -tags prod, embeds the real Vite bundle from the
// gitignored webdist/). Splitting the embed by build tag is what keeps the web build from ever
// dirtying a git-tracked file: the tracked placeholder and the generated bundle live in
// different directories.

// staticUI serves the embedded SPA: existing files are served directly, unknown
// paths fall back to index.html (client-side routing).
func staticUI() http.Handler {
	sub, err := fs.Sub(uiFS, uiRoot)
	if err != nil {
		// uiFS is a compile-time embed; fs.Sub on a constant valid path cannot fail.
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
			// A missing hashed asset is a real 404 — never fall back to index.html for it,
			// or the browser receives text/html for a .js/.css request and fails with a
			// confusing "not a valid MIME type" error (masking an index.html/assets hash
			// mismatch). The SPA fallback is only for client-side routes.
			if strings.HasPrefix(p, "/assets/") {
				http.NotFound(w, r)
				return
			}
			http.ServeFileFS(w, r, sub, "index.html") // SPA fallback for client routes
			return
		}
		files.ServeHTTP(w, r)
	})
}
