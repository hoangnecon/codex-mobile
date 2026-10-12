package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// StateManager manages persistent settings in ~/.codex/.codex-global-state.json
type StateManager struct {
	mu        sync.RWMutex
	statePath string
}

type WorkspaceRootsState struct {
	Order          []string          `json:"order"`
	Labels         map[string]string `json:"labels"`
	Active         []string          `json:"active"`
	ProjectOrder   []string          `json:"projectOrder,omitempty"`
	RemoteProjects []any             `json:"remoteProjects,omitempty"`
}

func NewStateManager() *StateManager {
	home, _ := os.UserHomeDir()
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	_ = os.MkdirAll(codexHome, 0755)
	return &StateManager{
		statePath: filepath.Join(codexHome, ".codex-global-state.json"),
	}
}

func (s *StateManager) readRaw() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.statePath)
	if err != nil {
		return make(map[string]any)
	}
	var res map[string]any
	if err := json.Unmarshal(data, &res); err != nil {
		return make(map[string]any)
	}
	return res
}

func (s *StateManager) writeRaw(data map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.statePath, b, 0644)
}

func (s *StateManager) GetWorkspaceRootsState() WorkspaceRootsState {
	raw := s.readRaw()
	order := toStringSlice(raw["electron-saved-workspace-roots"])
	labels := toStringMap(raw["electron-workspace-root-labels"])
	active := toStringSlice(raw["active-workspace-roots"])

	// Merge all local-projects from global state so all workspaces are discovered
	if localProjects, ok := raw["local-projects"].(map[string]any); ok {
		seen := make(map[string]bool)
		for _, o := range order {
			seen[o] = true
		}
		for _, v := range localProjects {
			if proj, ok := v.(map[string]any); ok {
				name, _ := proj["name"].(string)
				if rootPaths, ok := proj["rootPaths"].([]any); ok {
					for _, rp := range rootPaths {
						if p, ok := rp.(string); ok && p != "" {
							if !seen[p] {
								order = append(order, p)
								seen[p] = true
							}
							if name != "" && labels[p] == "" {
								labels[p] = name
							}
						}
					}
				}
			}
		}
	}

	state := WorkspaceRootsState{
		Order:  order,
		Labels: labels,
		Active: active,
	}
	if po, ok := raw["project-order"]; ok {
		state.ProjectOrder = toStringSlice(po)
	}
	return state
}

func (s *StateManager) SaveWorkspaceRootsState(st WorkspaceRootsState) error {
	raw := s.readRaw()
	raw["electron-saved-workspace-roots"] = st.Order
	raw["electron-workspace-root-labels"] = st.Labels
	raw["active-workspace-roots"] = st.Active
	if len(st.ProjectOrder) > 0 {
		raw["project-order"] = st.ProjectOrder
	}
	return s.writeRaw(raw)
}

func (s *StateManager) GetThreadTitles() map[string]string {
	raw := s.readRaw()
	return toStringMap(raw["thread-titles"])
}

func (s *StateManager) SaveThreadTitles(titles map[string]string) error {
	raw := s.readRaw()
	raw["thread-titles"] = titles
	return s.writeRaw(raw)
}

func (s *StateManager) GetThreadPins() []string {
	raw := s.readRaw()
	return toStringSlice(raw["pinned-threads"])
}

func (s *StateManager) SaveThreadPins(pins []string) error {
	raw := s.readRaw()
	raw["pinned-threads"] = pins
	return s.writeRaw(raw)
}

func toStringSlice(v any) []string {
	if v == nil {
		return []string{}
	}
	arr, ok := v.([]any)
	if !ok {
		return []string{}
	}
	res := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			res = append(res, s)
		}
	}
	return res
}

func toStringMap(v any) map[string]string {
	res := make(map[string]string)
	if v == nil {
		return res
	}
	m, ok := v.(map[string]any)
	if !ok {
		return res
	}
	for k, val := range m {
		if s, ok := val.(string); ok {
			res[k] = s
		}
	}
	return res
}
