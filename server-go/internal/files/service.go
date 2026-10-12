package files

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Service provides file and directory operations for Codex UI.
type Service struct {
	activeProjectRoot string
}

// NewService creates a new files Service.
func NewService() *Service {
	home, _ := os.UserHomeDir()
	return &Service{
		activeProjectRoot: home,
	}
}

func (s *Service) GetActiveProjectRoot() string {
	return s.activeProjectRoot
}

func (s *Service) SetActiveProjectRoot(root string) {
	s.activeProjectRoot = root
}

// ComposerSearch searches for files in a project root using ripgrep or WalkDir.
func (s *Service) ComposerSearch(root string, query string, maxResults int) ([]string, error) {
	if root == "" {
		root = s.activeProjectRoot
	}
	if maxResults <= 0 {
		maxResults = 50
	}

	var results []string

	// Try ripgrep first if available
	if rgPath, err := exec.LookPath("rg"); err == nil {
		args := []string{"--files", "--hidden", "-g", "!.git", "-g", "!node_modules"}
		cmd := exec.Command(rgPath, args...)
		cmd.Dir = root
		out, err := cmd.Output()
		if err == nil {
			scanner := bufio.NewScanner(bytes.NewReader(out))
			lowerQuery := strings.ToLower(query)
			for scanner.Scan() {
				line := scanner.Text()
				if query == "" || strings.Contains(strings.ToLower(line), lowerQuery) {
					results = append(results, line)
					if len(results) >= maxResults {
						break
					}
				}
			}
			return results, nil
		}
	}

	// Fallback to filepath.WalkDir
	lowerQuery := strings.ToLower(query)
	count := 0
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == "node_modules" || (strings.HasPrefix(name, ".") && name != ".") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		if query == "" || strings.Contains(strings.ToLower(rel), lowerQuery) {
			results = append(results, rel)
			count++
			if count >= maxResults {
				return io.EOF
			}
		}
		return nil
	})

	return results, nil
}

// DirectoryItem describes a directory entry for UI folder pickers.
type DirectoryItem struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	IsDirectory bool   `json:"isDirectory"`
}

// ListDirectory returns directory contents.
func (s *Service) ListDirectory(dirPath string, showHidden bool) ([]DirectoryItem, error) {
	if dirPath == "" {
		dirPath, _ = os.UserHomeDir()
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	var items []DirectoryItem
	for _, e := range entries {
		name := e.Name()
		if !showHidden && strings.HasPrefix(name, ".") {
			continue
		}
		items = append(items, DirectoryItem{
			Name:        name,
			Path:        filepath.Join(dirPath, name),
			IsDirectory: e.IsDir(),
		})
	}
	return items, nil
}

// HandleLocalImage serves local images with proper Content-Type.
func (s *Service) HandleLocalImage(w http.ResponseWriter, r *http.Request) {
	rawPath := r.URL.Query().Get("path")
	if rawPath == "" {
		http.Error(w, "missing path parameter", http.StatusBadRequest)
		return
	}

	cleanPath := filepath.Clean(rawPath)
	f, err := os.Open(cleanPath)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	defer f.Close()

	ext := filepath.Ext(cleanPath)
	ctype := mime.TypeByExtension(ext)
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = io.Copy(w, f)
}

// HandleLocalFile serves a local file inline.
func (s *Service) HandleLocalFile(w http.ResponseWriter, r *http.Request) {
	rawPath := r.URL.Query().Get("path")
	if rawPath == "" {
		http.Error(w, "missing path parameter", http.StatusBadRequest)
		return
	}

	cleanPath := filepath.Clean(rawPath)
	http.ServeFile(w, r, cleanPath)
}

// HandleLocalDirectories returns directory listing as JSON.
func (s *Service) HandleLocalDirectories(w http.ResponseWriter, r *http.Request) {
	rawPath := r.URL.Query().Get("path")
	showHidden := r.URL.Query().Get("showHidden") == "true"

	items, err := s.ListDirectory(rawPath, showHidden)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": items,
	})
}
