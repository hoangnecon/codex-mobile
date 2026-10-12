package account

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

type AccountInfo struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Provider string `json:"provider"`
	Plan     string `json:"plan"`
	IsActive bool   `json:"isActive"`
}

type Service struct {
	mu          sync.RWMutex
	storagePath string
}

func NewService() *Service {
	home, _ := os.UserHomeDir()
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	_ = os.MkdirAll(codexHome, 0755)
	return &Service{
		storagePath: filepath.Join(codexHome, "accounts.json"),
	}
}

func (s *Service) loadAccounts() []AccountInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.storagePath)
	if err != nil {
		return []AccountInfo{
			{
				ID:       "default",
				Email:    "local-user@codex.internal",
				Provider: "codex-app-server",
				Plan:     "Pro",
				IsActive: true,
			},
		}
	}
	var accounts []AccountInfo
	if err := json.Unmarshal(data, &accounts); err != nil || len(accounts) == 0 {
		return []AccountInfo{
			{
				ID:       "default",
				Email:    "local-user@codex.internal",
				Provider: "codex-app-server",
				Plan:     "Pro",
				IsActive: true,
			},
		}
	}
	return accounts
}

func (s *Service) saveAccounts(accs []AccountInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := json.MarshalIndent(accs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.storagePath, b, 0644)
}

func (s *Service) HandleList(w http.ResponseWriter, r *http.Request) {
	accounts := s.loadAccounts()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": accounts})
}

func (s *Service) HandleActive(w http.ResponseWriter, r *http.Request) {
	accounts := s.loadAccounts()
	for _, a := range accounts {
		if a.IsActive {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"data": a})
			return
		}
	}
	if len(accounts) > 0 {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": accounts[0]})
		return
	}
	http.Error(w, "no active account", http.StatusNotFound)
}

func (s *Service) HandleSwitch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	accounts := s.loadAccounts()
	found := false
	for i := range accounts {
		if accounts[i].ID == body.ID {
			accounts[i].IsActive = true
			found = true
		} else {
			accounts[i].IsActive = false
		}
	}
	if !found {
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}
	_ = s.saveAccounts(accounts)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (s *Service) HandleRefresh(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (s *Service) HandleRemove(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	accounts := s.loadAccounts()
	var updated []AccountInfo
	for _, a := range accounts {
		if a.ID != body.ID {
			updated = append(updated, a)
		}
	}
	_ = s.saveAccounts(updated)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}
