package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codex-server/internal/appserver"
)

type HubSkill struct {
	Name        string `json:"name"`
	Owner       string `json:"owner"`
	Description string `json:"description"`
	URL         string `json:"url"`
	Installed   bool   `json:"installed"`
	Path        string `json:"path,omitempty"`
	Enabled     bool   `json:"enabled"`
}

type rpcSkillsResponse struct {
	Data []struct {
		Cwd    string `json:"cwd"`
		Skills []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Path        string `json:"path"`
			Enabled     *bool  `json:"enabled"`
		} `json:"skills"`
	} `json:"data"`
}

// Service manages skills catalog and inspection for Codex Skills Hub.
type Service struct {
	bridge *appserver.Bridge
}

func NewService(bridge *appserver.Bridge) *Service {
	return &Service{
		bridge: bridge,
	}
}

// HandleSkillsHub returns installed skills in the format expected by SkillsHub.vue: { "installed": HubSkill[] }.
func (s *Service) HandleSkillsHub(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	resp, err := s.bridge.Call(ctx, "skills/list", map[string]any{})
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"installed": []HubSkill{}})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if resp.Error != nil || resp.Result == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"installed": []HubSkill{}})
		return
	}

	var rpcData rpcSkillsResponse
	if err := json.Unmarshal(resp.Result, &rpcData); err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"installed": []HubSkill{}})
		return
	}

	installed := make([]HubSkill, 0)
	seen := make(map[string]bool)
	for _, group := range rpcData.Data {
		for _, sk := range group.Skills {
			if sk.Name == "" || seen[sk.Name] {
				continue
			}
			seen[sk.Name] = true
			enabled := true
			if sk.Enabled != nil {
				enabled = *sk.Enabled
			}
			installed = append(installed, HubSkill{
				Name:        sk.Name,
				Owner:       "local",
				Description: sk.Description,
				URL:         "",
				Installed:   true,
				Path:        sk.Path,
				Enabled:     enabled,
			})
		}
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"installed": installed,
	})
}

// HandleSkillSearch returns search results for skills.
func (s *Service) HandleSkillSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	w.Header().Set("Content-Type", "application/json")
	if len(q) < 2 {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []HubSkill{}})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"results": []HubSkill{}})
}

// HandleSkillReadme reads the SKILL.md file for a skill path.
func (s *Service) HandleSkillReadme(w http.ResponseWriter, r *http.Request) {
	skillPath := r.URL.Query().Get("path")
	name := r.URL.Query().Get("name")
	if skillPath == "" && name != "" {
		home, _ := os.UserHomeDir()
		skillPath = filepath.Join(home, ".codex", "skills", name, "SKILL.md")
	}
	if skillPath == "" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"content": "", "description": ""})
		return
	}

	cleanPath := filepath.Clean(skillPath)
	if !strings.HasSuffix(cleanPath, "SKILL.md") {
		cleanPath = filepath.Join(cleanPath, "SKILL.md")
	}

	content, err := os.ReadFile(cleanPath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"content": fmt.Sprintf("# %s\n\nSkill documentation not found.", name), "description": ""})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"content":     string(content),
		"description": "",
		"source":      "local",
	})
}
