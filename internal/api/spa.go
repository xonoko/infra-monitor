package api

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

const uiMissing = `<!doctype html><html><head><meta charset="utf-8"><title>infra-monitor</title></head>
<body style="font-family:monospace;background:#0b0d10;color:#c9d1d9;padding:2rem">
<h1>infra-monitor</h1><p>The UI is not built into this binary. Run <code>make web</code> and rebuild.</p>
<p>API docs: <a style="color:#58a6ff" href="/api/v1/docs">/api/v1/docs</a></p></body></html>`

// spaHandler serves the embedded UI and falls back to index.html for client-side routes.
func spaHandler(files fs.FS) http.Handler {
	fileServer := http.FileServerFS(files)
	_, err := fs.Stat(files, "index.html")
	hasIndex := err == nil
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hasIndex {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(uiMissing))
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(files, p); err != nil {
			// client-side route
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			w.Header().Set("Cache-Control", "no-cache")
			fileServer.ServeHTTP(w, r2)
			return
		}
		if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	})
}
