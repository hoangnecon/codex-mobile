package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"codex-server/internal/account"
	"codex-server/internal/archive"
	"codex-server/internal/appserver"
	"codex-server/internal/auth"
	"codex-server/internal/events"
	"codex-server/internal/files"
	"codex-server/internal/git"
	"codex-server/internal/rpc"
	"codex-server/internal/skills"
	"codex-server/internal/telegram"
	"codex-server/internal/terminal"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Router struct {
	bridge   *appserver.Bridge
	hub      *events.Hub
	filesSvc *files.Service
	gitSvc   *git.Service
	skillsSvc *skills.Service
	archiveSvc *archive.Service
	termMgr  *terminal.Manager
	accountSvc *account.Service
	authMgr  *auth.Manager
	telegramBr *telegram.Bridge
	state    *StateManager
	chiMux   *chi.Mux
}

func NewRouter(
	bridge *appserver.Bridge,
	hub *events.Hub,
	filesSvc *files.Service,
	gitSvc *git.Service,
	skillsSvc *skills.Service,
	archiveSvc *archive.Service,
	termMgr *terminal.Manager,
	accountSvc *account.Service,
	authMgr *auth.Manager,
	telegramBr *telegram.Bridge,
) *Router {
	r := &Router{
		bridge:   bridge,
		hub:      hub,
		filesSvc: filesSvc,
		gitSvc:   gitSvc,
		skillsSvc: skillsSvc,
		archiveSvc: archiveSvc,
		termMgr:  termMgr,
		accountSvc: accountSvc,
		authMgr:  authMgr,
		telegramBr: telegramBr,
		state:    NewStateManager(),
		chiMux:   chi.NewRouter(),
	}

	// Connect Bridge notifications to realtime Hub
	bridge.SubscribeNotification(func(n rpc.Notification) {
		hub.Broadcast(n)
	})

	r.setupRoutes()
	return r
}

func (r *Router) Mux() *chi.Mux {
	return r.chiMux
}

func (r *Router) setupRoutes() {
	mux := r.chiMux

	mux.Use(middleware.RequestID)
	mux.Use(middleware.RealIP)
	mux.Use(middleware.Recoverer)
	mux.Use(corsMiddleware)
	if r.authMgr != nil && r.authMgr.IsEnabled() {
		mux.Use(r.authMgr.Middleware)
	}

	// Auth
	mux.Post("/codex-api/auth/login", r.handleLogin)

	// Realtime WebSocket & SSE
	mux.Get("/codex-api/ws", r.hub.HandleWebSocket)
	mux.Get("/codex-api/events", r.hub.HandleSSE)

	// RPC Forwarder
	mux.Post("/codex-api/rpc", r.handleRPC)

	// Thread specific turn & state endpoints
	mux.Get("/codex-api/thread-turn-page", r.handleThreadTurnPage)
	mux.Get("/codex-api/thread-file-change-fallback", r.handleThreadFileChangeFallback)
	mux.Get("/codex-api/thread-live-state", r.handleThreadLiveState)
	mux.Get("/codex-api/provider-models", r.handleProviderModels)

	// Workspace & Project state
	mux.Get("/codex-api/workspace-roots-state", r.handleGetWorkspaceRoots)
	mux.Put("/codex-api/workspace-roots-state", r.handlePutWorkspaceRoots)
	mux.Get("/codex-api/home-directory", r.handleGetHomeDirectory)
	mux.Post("/codex-api/local-directory", r.handlePostLocalDirectory)
	mux.Post("/codex-api/project-root", r.handleSetProjectRoot)
	mux.Get("/codex-api/project-root-suggestion", r.handleProjectRootSuggestion)

	// Thread metadata
	mux.Get("/codex-api/thread-titles", r.handleGetThreadTitles)
	mux.Put("/codex-api/thread-titles", r.handlePutThreadTitles)
	mux.Get("/codex-api/thread-pins", r.handleGetThreadPins)
	mux.Put("/codex-api/thread-pins", r.handlePutThreadPins)

	// File Operations
	mux.Post("/codex-api/composer-file-search", r.handleComposerFileSearch)
	mux.Post("/codex-api/upload-file", r.handleUploadFile)
	mux.Post("/codex-api/transcribe", r.handleTranscribe)

	// Git & Worktree routes
	mux.Get("/codex-api/git/repository-status", r.handleGitRepoStatus)
	mux.Get("/codex-api/git/branches", r.handleGitBranches)
	mux.Get("/codex-api/git/branch-commits", r.handleGitCommits)
	mux.Post("/codex-api/git/checkout", r.handleGitCheckout)

	// Skills Hub routes
	mux.Get("/codex-api/skills-hub", r.skillsSvc.HandleSkillsHub)
	mux.Get("/codex-api/skills-hub/search", r.skillsSvc.HandleSkillSearch)
	mux.Get("/codex-api/skills-hub/readme", r.skillsSvc.HandleSkillReadme)

	// Metadata & Discovery routes expected on startup
	mux.Get("/codex-api/meta/methods", r.handleMetaMethods)
	mux.Get("/codex-api/meta/notifications", r.handleMetaNotifications)
	mux.Get("/codex-api/server-requests/pending", r.handlePendingServerRequests)
	mux.Get("/codex-api/thread-queue-state", r.handleThreadQueueState)
	mux.Put("/codex-api/thread-queue-state", r.handleThreadQueueStatePut)
	mux.Get("/codex-api/preferences/first-launch-plugins-card", r.handleFirstLaunchPluginsCard)
	mux.Put("/codex-api/preferences/first-launch-plugins-card", r.handleFirstLaunchPluginsCardPut)

	// Project ZIP archive routes
	mux.Get("/codex-api/project-zip", r.archiveSvc.ExportProjectZip)
	mux.Post("/codex-api/project-import", r.archiveSvc.ImportProjectZip)

	// Terminal PTY routes
	mux.Get("/codex-api/thread-terminal/status", r.handleTerminalStatus)
	mux.Get("/codex-api/thread-terminal/quick-commands", r.handleTerminalQuickCommands)
	mux.Post("/codex-api/thread-terminal/attach", r.handleTerminalAttach)
	mux.Post("/codex-api/thread-terminal/input", r.handleTerminalInput)
	mux.Post("/codex-api/thread-terminal/resize", r.handleTerminalResize)
	mux.Post("/codex-api/thread-terminal/close", r.handleTerminalClose)
	mux.Get("/codex-api/thread-terminal-snapshot", r.handleTerminalSnapshot)

	// Account routes
	mux.Get("/codex-api/accounts", r.accountSvc.HandleList)
	mux.Get("/codex-api/accounts/active", r.accountSvc.HandleActive)
	mux.Post("/codex-api/accounts/switch", r.accountSvc.HandleSwitch)
	mux.Post("/codex-api/accounts/refresh", r.accountSvc.HandleRefresh)
	mux.Post("/codex-api/accounts/remove", r.accountSvc.HandleRemove)

	// Telegram routes
	mux.Get("/codex-api/telegram/status", r.telegramBr.HandleStatus)
	mux.Get("/codex-api/telegram/config", r.telegramBr.HandleConfig)
	mux.Post("/codex-api/telegram/configure-bot", r.telegramBr.HandleConfigureBot)

	// Local browsing & viewing
	mux.Get("/codex-local-image", r.filesSvc.HandleLocalImage)
	mux.Get("/codex-local-file", r.filesSvc.HandleLocalFile)
	mux.Get("/codex-local-directories", r.filesSvc.HandleLocalDirectories)
	mux.Get("/codex-local-browse/*", r.handleLocalBrowse)
	mux.Get("/codex-local-edit/*", r.handleLocalEditGet)
	mux.Put("/codex-local-edit/*", r.handleLocalEditPut)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type rpcCallPayload struct {
	Method string `json:"method"`
	Params any    `json:"params"`
}

func (r *Router) handleRPC(w http.ResponseWriter, req *http.Request) {
	var payload rpcCallPayload
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}

	if payload.Method == "" {
		http.Error(w, `{"error":"missing method field"}`, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(req.Context(), 120*time.Second)
	defer cancel()

	resp, err := r.bridge.Call(ctx, payload.Method, payload.Params)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if resp.Error != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": resp.Error,
		})
		return
	}

	// If Result is nil, return empty object
	if resp.Result == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"result": nil})
		return
	}

	// For thread/read and thread/resume, trim turns to the latest 10 turns to keep payload lightweight and snappy
	if payload.Method == "thread/read" || payload.Method == "thread/resume" {
		var threadResp map[string]any
		if err := json.Unmarshal(resp.Result, &threadResp); err == nil {
			if thread, ok := threadResp["thread"].(map[string]any); ok {
				if turns, ok := thread["turns"].([]any); ok && len(turns) > 10 {
					startIndex := len(turns) - 10
					thread["turns"] = turns[startIndex:]
					threadResp["threadTurnStartIndex"] = startIndex
					_ = json.NewEncoder(w).Encode(map[string]any{
						"result": threadResp,
					})
					return
				}
			}
		}
	}

	// For thread/list, deduplicate threads with the same ID, keeping the latest updatedAt
	if payload.Method == "thread/list" {
		var listResp struct {
			Data       []map[string]any `json:"data"`
			NextCursor *string          `json:"nextCursor"`
		}
		if err := json.Unmarshal(resp.Result, &listResp); err == nil && len(listResp.Data) > 0 {
			deduped := make([]map[string]any, 0, len(listResp.Data))
			seen := make(map[string]int)
			for _, item := range listResp.Data {
				id, ok := item["id"].(string)
				if !ok || id == "" {
					deduped = append(deduped, item)
					continue
				}
				if prevIdx, found := seen[id]; found {
					prev := deduped[prevIdx]
					prevUpdated, _ := prev["updatedAt"].(float64)
					currUpdated, _ := item["updatedAt"].(float64)
					if currUpdated > prevUpdated {
						deduped[prevIdx] = item
					}
				} else {
					seen[id] = len(deduped)
					deduped = append(deduped, item)
				}
			}
			listResp.Data = deduped
			if dedupedBytes, err := json.Marshal(listResp); err == nil {
				resp.Result = dedupedBytes
			}
		}
	}

	// Wrap in { "result": ... } envelope as expected by frontend RpcEnvelope
	_, _ = w.Write([]byte(`{"result":`))
	_, _ = w.Write(resp.Result)
	_, _ = w.Write([]byte(`}`))
}

func (r *Router) handleGetWorkspaceRoots(w http.ResponseWriter, req *http.Request) {
	state := r.state.GetWorkspaceRootsState()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": state,
	})
}

func (r *Router) handlePutWorkspaceRoots(w http.ResponseWriter, req *http.Request) {
	var st WorkspaceRootsState
	if err := json.NewDecoder(req.Body).Decode(&st); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := r.state.SaveWorkspaceRootsState(st); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (r *Router) handleGetHomeDirectory(w http.ResponseWriter, req *http.Request) {
	home, _ := os.UserHomeDir()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": map[string]string{"path": home},
	})
}

func (r *Router) handlePostLocalDirectory(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Path       string `json:"path"`
		ShowHidden bool   `json:"showHidden"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)
	items, err := r.filesSvc.ListDirectory(body.Path, body.ShowHidden)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": items})
}

func (r *Router) handleSetProjectRoot(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Root string `json:"root"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	r.filesSvc.SetActiveProjectRoot(body.Root)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (r *Router) handleProjectRootSuggestion(w http.ResponseWriter, req *http.Request) {
	home, _ := os.UserHomeDir()
	suggestion := "New Project"
	for i := 1; i < 100; i++ {
		candidate := filepath.Join(home, fmt.Sprintf("New Project (%d)", i))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			suggestion = fmt.Sprintf("New Project (%d)", i)
			break
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": map[string]string{"suggestion": suggestion},
	})
}

func (r *Router) handleGetThreadTitles(w http.ResponseWriter, req *http.Request) {
	titles := r.state.GetThreadTitles()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": titles})
}

func (r *Router) handlePutThreadTitles(w http.ResponseWriter, req *http.Request) {
	var titles map[string]string
	if err := json.NewDecoder(req.Body).Decode(&titles); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = r.state.SaveThreadTitles(titles)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (r *Router) handleGetThreadPins(w http.ResponseWriter, req *http.Request) {
	pins := r.state.GetThreadPins()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": map[string]any{"threadIds": pins},
	})
}

func (r *Router) handlePutThreadPins(w http.ResponseWriter, req *http.Request) {
	var body struct {
		ThreadIds []string `json:"threadIds"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = r.state.SaveThreadPins(body.ThreadIds)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (r *Router) handleComposerFileSearch(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Query string `json:"query"`
		Root  string `json:"root"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)
	results, err := r.filesSvc.ComposerSearch(body.Root, body.Query, 50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"files": results,
	})
}

func (r *Router) handleUploadFile(w http.ResponseWriter, req *http.Request) {
	err := req.ParseMultipartForm(50 << 20) // 50MB
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	file, header, err := req.FormFile("file")
	if err != nil {
		http.Error(w, "missing file form field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	targetDir := r.filesSvc.GetActiveProjectRoot()
	targetPath := filepath.Join(targetDir, header.Filename)
	out, err := os.Create(targetPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer out.Close()

	_, _ = io.Copy(out, file)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":   true,
		"path": targetPath,
	})
}

func (r *Router) handleTranscribe(w http.ResponseWriter, req *http.Request) {
	// Voice Dictation handler
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"text": "",
	})
}

func (r *Router) handleGitRepoStatus(w http.ResponseWriter, req *http.Request) {
	cwd := req.URL.Query().Get("cwd")
	if cwd == "" {
		cwd = r.filesSvc.GetActiveProjectRoot()
	}
	status, err := r.gitSvc.GetRepoStatus(cwd)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": status})
}

func (r *Router) handleGitBranches(w http.ResponseWriter, req *http.Request) {
	cwd := req.URL.Query().Get("cwd")
	if cwd == "" {
		cwd = r.filesSvc.GetActiveProjectRoot()
	}
	branches, err := r.gitSvc.ListBranches(cwd)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": branches})
}

func (r *Router) handleGitCommits(w http.ResponseWriter, req *http.Request) {
	cwd := req.URL.Query().Get("cwd")
	if cwd == "" {
		cwd = r.filesSvc.GetActiveProjectRoot()
	}
	commits, err := r.gitSvc.ListCommits(cwd, 30)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": commits})
}

func (r *Router) handleGitCheckout(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Cwd    string `json:"cwd"`
		Branch string `json:"branch"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.Cwd == "" {
		body.Cwd = r.filesSvc.GetActiveProjectRoot()
	}
	if err := r.gitSvc.Checkout(body.Cwd, body.Branch); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (r *Router) handleLogin(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)
	token, ok := r.authMgr.VerifyPassword(body.Password)
	if !ok {
		http.Error(w, `{"error":"invalid password"}`, http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "codex_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   7 * 24 * 3600,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "token": token})
}

func (r *Router) handleTerminalStatus(w http.ResponseWriter, req *http.Request) {
	threadID := req.URL.Query().Get("threadId")
	if threadID == "" {
		threadID = "default"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":       true,
		"threadId": threadID,
		"active":   true,
	})
}

func (r *Router) handleTerminalQuickCommands(w http.ResponseWriter, req *http.Request) {
	cmds := []map[string]string{
		{"label": "Git Status", "command": "git status"},
		{"label": "List Files", "command": "ls -la"},
		{"label": "Run Tests", "command": "go test ./..."},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": cmds})
}

func (r *Router) handleTerminalAttach(w http.ResponseWriter, req *http.Request) {
	var body struct {
		ThreadID string `json:"threadId"`
		Cwd      string `json:"cwd"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)
	if body.ThreadID == "" {
		body.ThreadID = "default"
	}
	if body.Cwd == "" {
		body.Cwd = r.filesSvc.GetActiveProjectRoot()
	}
	_, err := r.termMgr.GetOrCreate(body.ThreadID, body.Cwd)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (r *Router) handleTerminalInput(w http.ResponseWriter, req *http.Request) {
	var body struct {
		ThreadID string `json:"threadId"`
		Data     string `json:"data"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)
	if body.ThreadID == "" {
		body.ThreadID = "default"
	}
	err := r.termMgr.WriteInput(body.ThreadID, []byte(body.Data))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (r *Router) handleTerminalResize(w http.ResponseWriter, req *http.Request) {
	var body struct {
		ThreadID string `json:"threadId"`
		Cols     uint16 `json:"cols"`
		Rows     uint16 `json:"rows"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)
	if body.ThreadID == "" {
		body.ThreadID = "default"
	}
	_ = r.termMgr.Resize(body.ThreadID, body.Cols, body.Rows)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (r *Router) handleTerminalClose(w http.ResponseWriter, req *http.Request) {
	var body struct {
		ThreadID string `json:"threadId"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)
	if body.ThreadID == "" {
		body.ThreadID = "default"
	}
	_ = r.termMgr.Close(body.ThreadID)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (r *Router) handleTerminalSnapshot(w http.ResponseWriter, req *http.Request) {
	threadID := req.URL.Query().Get("threadId")
	if threadID == "" {
		threadID = "default"
	}
	snapshot := r.termMgr.GetSnapshot(threadID)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"snapshot": string(snapshot),
	})
}

func (r *Router) handleMetaMethods(w http.ResponseWriter, req *http.Request) {
	methods := []string{
		"initialize", "model/list", "skills/list", "thread/start", "thread/resume",
		"thread/read", "thread/list", "turn/start", "turn/interrupt", "turn/steer",
		"account/rateLimits", "server/diagnostics",
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": methods})
}

func (r *Router) handleMetaNotifications(w http.ResponseWriter, req *http.Request) {
	notifications := []string{
		"turn/started", "turn/completed", "thread/status", "thread/updated",
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": notifications})
}

func (r *Router) handlePendingServerRequests(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
}

func (r *Router) handleThreadQueueState(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": map[string]any{
			"threads": map[string]any{},
		},
	})
}

func (r *Router) handleThreadQueueStatePut(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (r *Router) handleFirstLaunchPluginsCard(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": map[string]any{
			"dismissed": true,
		},
	})
}

func (r *Router) handleFirstLaunchPluginsCardPut(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (r *Router) handleThreadTurnPage(w http.ResponseWriter, req *http.Request) {
	threadId := req.URL.Query().Get("threadId")
	if threadId == "" {
		http.Error(w, `{"error":"Missing threadId"}`, http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
	defer cancel()

	resp, err := r.bridge.Call(ctx, "thread/read", map[string]any{
		"threadId":     threadId,
		"includeTurns": true,
	})
	if err != nil || resp == nil || resp.Error != nil {
		http.Error(w, `{"error":"Failed to read thread"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"result":`))
	_, _ = w.Write(resp.Result)
	_, _ = w.Write([]byte(`}`))
}

func (r *Router) handleThreadFileChangeFallback(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"result": map[string]any{
			"fileChanges": []any{},
		},
	})
}

func (r *Router) handleThreadLiveState(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"result": map[string]any{},
	})
}

func (r *Router) handleProviderModels(w http.ResponseWriter, req *http.Request) {
	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()

	resp, err := r.bridge.Call(ctx, "model/list", map[string]any{})
	if err != nil || resp == nil || resp.Error != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []string{
				"google-antigravity/gemini-3.8-flash",
				"google-antigravity/gemini-3.7-flash",
				"google-antigravity/claude-sonnet-5-5",
				"gpt-6-astra", "gpt-6-sol", "gpt-6-luna",
				"deepseek-flash", "deepseek-v4-pro",
			},
			"source": "fallback",
		})
		return
	}

	var listResult struct {
		Data []struct {
			ID    string `json:"id"`
			Model string `json:"model"`
		} `json:"data"`
	}
	_ = json.Unmarshal(resp.Result, &listResult)

	var ids []string
	for _, m := range listResult.Data {
		name := m.ID
		if name == "" {
			name = m.Model
		}
		if name != "" {
			ids = append(ids, name)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	var rawModels []any
	var fullResult map[string]any
	if err := json.Unmarshal(resp.Result, &fullResult); err == nil {
		if d, ok := fullResult["data"].([]any); ok {
			rawModels = d
		}
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":      ids,
		"models":    rawModels,
		"source":    "provider",
		"exclusive": false,
	})
}

func (r *Router) handleLocalBrowse(w http.ResponseWriter, req *http.Request) {
	path := chi.URLParam(req, "*")
	cleanPath := filepath.Clean("/" + path)
	info, err := os.Stat(cleanPath)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if info.IsDir() {
		items, _ := r.filesSvc.ListDirectory(cleanPath, true)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<html><body><h2>Index of %s</h2><ul>", cleanPath)
		for _, item := range items {
			fmt.Fprintf(w, "<li><a href=\"/codex-local-browse%s\">%s</a></li>", item.Path, item.Name)
		}
		fmt.Fprintf(w, "</ul></body></html>")
		return
	}
	http.ServeFile(w, req, cleanPath)
}

func (r *Router) handleLocalEditGet(w http.ResponseWriter, req *http.Request) {
	path := chi.URLParam(req, "*")
	cleanPath := filepath.Clean("/" + path)
	content, err := os.ReadFile(cleanPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(content)
}

func (r *Router) handleLocalEditPut(w http.ResponseWriter, req *http.Request) {
	path := chi.URLParam(req, "*")
	cleanPath := filepath.Clean("/" + path)
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := os.WriteFile(cleanPath, body, 0644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}
