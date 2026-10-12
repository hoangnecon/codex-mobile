package archive

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Service provides Project ZIP export and import.
type Service struct{}

func NewService() *Service {
	return &Service{}
}

// ExportProjectZip streams a zip archive of the given directory.
func (s *Service) ExportProjectZip(w http.ResponseWriter, r *http.Request) {
	projectRoot := r.URL.Query().Get("root")
	if projectRoot == "" {
		http.Error(w, "missing root query parameter", http.StatusBadRequest)
		return
	}

	projectRoot = filepath.Clean(projectRoot)
	info, err := os.Stat(projectRoot)
	if err != nil || !info.IsDir() {
		http.Error(w, "invalid project directory", http.StatusBadRequest)
		return
	}

	projectName := filepath.Base(projectRoot)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.zip\"", projectName))

	zw := zip.NewWriter(w)
	defer zw.Close()

	_ = filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		name := info.Name()
		if info.IsDir() {
			if name == ".git" || name == "node_modules" || name == "dist" || name == ".next" {
				return filepath.SkipDir
			}
			return nil
		}
		if name == ".DS_Store" {
			return nil
		}

		rel, err := filepath.Rel(projectRoot, path)
		if err != nil {
			return nil
		}

		zipHeader, err := zip.FileInfoHeader(info)
		if err != nil {
			return nil
		}
		zipHeader.Name = filepath.ToSlash(rel)
		zipHeader.Method = zip.Deflate

		writer, err := zw.CreateHeader(zipHeader)
		if err != nil {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()

		_, _ = io.Copy(writer, file)
		return nil
	})
}

// ImportProjectZip unzips an uploaded project zip.
func (s *Service) ImportProjectZip(w http.ResponseWriter, r *http.Request) {
	err := r.ParseMultipartForm(100 << 20) // 100MB
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing zip file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	targetDir := r.FormValue("targetDir")
	if targetDir == "" {
		home, _ := os.UserHomeDir()
		stem := strings.TrimSuffix(header.Filename, ".zip")
		targetDir = filepath.Join(home, stem)
	}
	_ = os.MkdirAll(targetDir, 0755)

	// Read to temp file
	tmpFile, err := os.CreateTemp("", "codex-import-*.zip")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	_, _ = io.Copy(tmpFile, file)

	zr, err := zip.OpenReader(tmpFile.Name())
	if err != nil {
		http.Error(w, "failed to read zip", http.StatusBadRequest)
		return
	}
	defer zr.Close()

	for _, f := range zr.File {
		clean := filepath.Clean(f.Name)
		if strings.HasPrefix(clean, "..") {
			continue
		}
		outPath := filepath.Join(targetDir, clean)
		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(outPath, 0755)
			continue
		}
		_ = os.MkdirAll(filepath.Dir(outPath), 0755)
		rc, err := f.Open()
		if err != nil {
			continue
		}
		outFile, err := os.Create(outPath)
		if err == nil {
			_, _ = io.Copy(outFile, rc)
			outFile.Close()
		}
		rc.Close()
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(fmt.Sprintf(`{"ok":true,"targetDir":"%s"}`, targetDir)))
}
