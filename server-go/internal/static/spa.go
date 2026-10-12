package static

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// SPAServer serves a single-page application (SPA) with fallback to index.html.
type SPAServer struct {
	staticDir string
	embedFS   fs.FS
}

// NewSPAServer creates a SPA server given a disk directory path and an optional embed.FS.
func NewSPAServer(staticDir string, embedFS fs.FS) *SPAServer {
	return &SPAServer{
		staticDir: staticDir,
		embedFS:   embedFS,
	}
}

func (s *SPAServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Don't intercept API routes
	if strings.HasPrefix(path, "/codex-api/") || strings.HasPrefix(path, "/codex-local-") {
		http.NotFound(w, r)
		return
	}

	// If staticDir on disk is provided and exists, serve from disk
	if s.staticDir != "" {
		cleanPath := filepath.Clean(filepath.Join(s.staticDir, filepath.FromSlash(path)))
		// Prevent directory traversal
		if strings.HasPrefix(cleanPath, filepath.Clean(s.staticDir)) {
			info, err := os.Stat(cleanPath)
			if err == nil && !info.IsDir() {
				http.ServeFile(w, r, cleanPath)
				return
			}
		}

		// SPA fallback: index.html
		indexPath := filepath.Join(s.staticDir, "index.html")
		if _, err := os.Stat(indexPath); err == nil {
			http.ServeFile(w, r, indexPath)
			return
		}
	}

	// If embedded FS is available, serve from embedFS
	if s.embedFS != nil {
		trimmedPath := strings.TrimPrefix(path, "/")
		if trimmedPath == "" {
			trimmedPath = "index.html"
		}

		f, err := s.embedFS.Open(trimmedPath)
		if err == nil {
			stat, _ := f.Stat()
			if !stat.IsDir() {
				_ = f.Close()
				http.FileServer(http.FS(s.embedFS)).ServeHTTP(w, r)
				return
			}
			_ = f.Close()
		}

		// Fallback to index.html in embedFS
		indexF, err := s.embedFS.Open("index.html")
		if err == nil {
			_ = indexF.Close()
			r.URL.Path = "/index.html"
			http.FileServer(http.FS(s.embedFS)).ServeHTTP(w, r)
			return
		}
	}

	// Neither disk nor embed available: render simple fallback
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!doctype html><html><head><title>Codex Server</title></head><body style="font-family:sans-serif;background:#09090b;color:#e4e4e7;padding:40px;text-align:center;"><h2>Codex App-Server Bridge (Golang)</h2><p>Backend is active. Frontend assets not yet built or provided.</p><p><a href="/codex-api/events" style="color:#10b981;">Check /codex-api/events</a></p></body></html>`))
}
