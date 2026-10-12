package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"codex-server/internal/account"
	"codex-server/internal/archive"
	"codex-server/internal/appserver"
	"codex-server/internal/auth"
	"codex-server/internal/events"
	"codex-server/internal/files"
	"codex-server/internal/git"
	"codex-server/internal/skills"
	"codex-server/internal/telegram"
	"codex-server/internal/terminal"
)

func TestGetWorkspaceRoots(t *testing.T) {
	bridge := appserver.NewBridge(appserver.Config{})
	hub := events.NewHub()
	filesSvc := files.NewService()
	gitSvc := git.NewService()
	skillsSvc := skills.NewService(bridge)
	archiveSvc := archive.NewService()
	termMgr := terminal.NewManager()
	accountSvc := account.NewService()
	authMgr := auth.NewManager("")
	telegramBr := telegram.NewBridge(bridge)
	router := NewRouter(bridge, hub, filesSvc, gitSvc, skillsSvc, archiveSvc, termMgr, accountSvc, authMgr, telegramBr)

	req := httptest.NewRequest("GET", "/codex-api/workspace-roots-state", nil)
	rec := httptest.NewRecorder()

	router.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if body == "" {
		t.Fatal("expected non-empty body")
	}
}

func TestGetHomeDirectory(t *testing.T) {
	bridge := appserver.NewBridge(appserver.Config{})
	hub := events.NewHub()
	filesSvc := files.NewService()
	gitSvc := git.NewService()
	skillsSvc := skills.NewService(bridge)
	archiveSvc := archive.NewService()
	termMgr := terminal.NewManager()
	accountSvc := account.NewService()
	authMgr := auth.NewManager("")
	telegramBr := telegram.NewBridge(bridge)
	router := NewRouter(bridge, hub, filesSvc, gitSvc, skillsSvc, archiveSvc, termMgr, accountSvc, authMgr, telegramBr)

	req := httptest.NewRequest("GET", "/codex-api/home-directory", nil)
	rec := httptest.NewRecorder()

	router.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
}
