package lab

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ContentSecurityPolicy is the console's baseline. The compiled interface
// needs inline styles for charts, but no inline script, external origin or
// framing.
const ContentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// WithWeb serves an explicitly supplied real build. It never installs the demo API.
// Browser tests inject the laboratory credential; production login is separate.
func WithWeb(api http.Handler, directory string) (http.Handler, error) {
	root, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(filepath.Join(root, "index.html")); err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("--web-dir must contain a compiled index.html")
	}
	if info, err := os.Stat(filepath.Join(root, "assets")); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("--web-dir must contain compiled assets")
	}
	files := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			api.ServeHTTP(w, r)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			http.Error(w, "method not allowed", 405)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", ContentSecurityPolicy)
		w.Header().Set("Cache-Control", "no-cache")
		// Files are selected by FileServer's cleaned URL path; history routes receive only index.html.
		if strings.HasPrefix(r.URL.Path, "/assets/") || strings.Contains(filepath.Base(r.URL.Path), ".") {
			allowed := map[string]bool{".js": true, ".css": true, ".woff": true, ".woff2": true, ".svg": true, ".png": true, ".jpg": true, ".webp": true, ".ico": true}
			for _, part := range strings.Split(r.URL.Path, "/") {
				if strings.HasPrefix(part, ".") {
					http.NotFound(w, r)
					return
				}
			}
			if !allowed[filepath.Ext(r.URL.Path)] {
				http.NotFound(w, r)
				return
			}
			files.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(root, "index.html"))
	}), nil
}
