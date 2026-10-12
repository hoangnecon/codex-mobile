package files

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListDirectory(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "file1.txt"), []byte("hello"), 0644)
	_ = os.Mkdir(filepath.Join(dir, "subdir"), 0755)

	svc := NewService()
	items, err := svc.ListDirectory(dir, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
}

func TestComposerSearch(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# Hello"), 0644)

	svc := NewService()
	results, err := svc.ComposerSearch(dir, "main", 10)
	if err != nil {
		t.Fatalf("unexpected search error: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected to find main.go, got %v", results)
	}
}
