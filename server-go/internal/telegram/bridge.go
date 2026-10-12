package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"codex-server/internal/appserver"
)

type Config struct {
	BotToken       string   `json:"botToken"`
	AllowedUserIDs []string `json:"allowedUserIds"`
}

type Bridge struct {
	mu             sync.RWMutex
	token          string
	allowedUserIDs map[string]struct{}
	running        bool
	stopCh         chan struct{}
	appServer      *appserver.Bridge
	activeThreadID string
	configPath     string
}

func NewBridge(appServer *appserver.Bridge) *Bridge {
	home, _ := os.UserHomeDir()
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}

	b := &Bridge{
		appServer:      appServer,
		allowedUserIDs: make(map[string]struct{}),
		configPath:     filepath.Join(codexHome, "telegram-bridge.json"),
	}

	b.loadConfig()
	if token := os.Getenv("TELEGRAM_BOT_TOKEN"); token != "" {
		b.token = token
	}
	if users := os.Getenv("TELEGRAM_ALLOWED_USER_IDS"); users != "" {
		b.allowedUserIDs = make(map[string]struct{})
		b.allowedUserIDs[users] = struct{}{}
	}

	return b
}

func (b *Bridge) loadConfig() {
	data, err := os.ReadFile(b.configPath)
	if err != nil {
		return
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err == nil {
		b.token = cfg.BotToken
		for _, u := range cfg.AllowedUserIDs {
			b.allowedUserIDs[u] = struct{}{}
		}
	}
}

func (b *Bridge) SaveConfig(token string, users []string) error {
	b.mu.Lock()
	b.token = token
	b.allowedUserIDs = make(map[string]struct{})
	for _, u := range users {
		b.allowedUserIDs[u] = struct{}{}
	}
	b.mu.Unlock()

	cfg := Config{
		BotToken:       token,
		AllowedUserIDs: users,
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(b.configPath, data, 0644)
}

func (b *Bridge) Start() {
	b.mu.Lock()
	if b.running || b.token == "" {
		b.mu.Unlock()
		return
	}
	b.running = true
	b.stopCh = make(chan struct{})
	b.mu.Unlock()

	go b.pollLoop()
}

func (b *Bridge) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.running {
		return
	}
	b.running = false
	close(b.stopCh)
}

type tgUpdate struct {
	UpdateID int `json:"update_id"`
	Message  *struct {
		MessageID int `json:"message_id"`
		From      struct {
			ID int64 `json:"id"`
		} `json:"from"`
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		Text string `json:"text"`
	} `json:"message"`
}

func (b *Bridge) pollLoop() {
	offset := 0
	client := &http.Client{Timeout: 35 * time.Second}

	for {
		b.mu.RLock()
		running := b.running
		token := b.token
		b.mu.RUnlock()
		if !running || token == "" {
			return
		}

		url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=30", token, offset)
		resp, err := client.Get(url)
		if err != nil {
			time.Sleep(3 * time.Second)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		var result struct {
			OK     bool       `json:"ok"`
			Result []tgUpdate `json:"result"`
		}
		if err := json.Unmarshal(body, &result); err == nil && result.OK {
			for _, u := range result.Result {
				if u.UpdateID >= offset {
					offset = u.UpdateID + 1
				}
				if u.Message != nil && u.Message.Text != "" {
					b.handleMessage(u.Message.Chat.ID, u.Message.From.ID, u.Message.Text)
				}
			}
		} else {
			time.Sleep(2 * time.Second)
		}
	}
}

func (b *Bridge) handleMessage(chatID int64, fromID int64, text string) {
	b.mu.RLock()
	token := b.token
	allowed := b.allowedUserIDs
	b.mu.RUnlock()

	// Verify user ID if allowlist configured
	if len(allowed) > 0 {
		if _, ok := allowed[fmt.Sprintf("%d", fromID)]; !ok {
			b.sendMessage(token, chatID, "Unauthorized user.")
			return
		}
	}

	switch text {
	case "/start":
		b.sendMessage(token, chatID, "👋 Welcome to Codex Telegram Bridge!\nSend any message to prompt your active AI Agent.")
		return
	case "/status":
		b.sendMessage(token, chatID, "🟢 Codex Bridge is active and connected to local Go server.")
		return
	case "/help":
		b.sendMessage(token, chatID, "Commands:\n/start - welcome\n/status - bridge status\n/help - this help\n\nAny other text is forwarded to Codex.")
		return
	}

	// Forward prompt to Codex App Server
	go func() {
		b.sendMessage(token, chatID, "Thinking...")
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		// Start turn
		turnParams := map[string]any{
			"message": text,
		}
		resp, err := b.appServer.Call(ctx, "turn/start", turnParams)
		if err != nil {
			b.sendMessage(token, chatID, fmt.Sprintf("Error: %v", err))
			return
		}
		if resp.Error != nil {
			b.sendMessage(token, chatID, fmt.Sprintf("Error: %s", resp.Error.Message))
			return
		}
		b.sendMessage(token, chatID, "Response completed.")
	}()
}

func (b *Bridge) sendMessage(token string, chatID int64, text string) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	payload := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[telegram] send message failed: %v", err)
		return
	}
	_ = resp.Body.Close()
}

func (b *Bridge) HandleStatus(w http.ResponseWriter, r *http.Request) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"running":   b.running,
		"hasToken":  b.token != "",
		"userCount": len(b.allowedUserIDs),
	})
}

func (b *Bridge) HandleConfig(w http.ResponseWriter, r *http.Request) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var users []string
	for u := range b.allowedUserIDs {
		users = append(users, u)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"hasToken":       b.token != "",
		"allowedUserIds": users,
	})
}

func (b *Bridge) HandleConfigureBot(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BotToken       string   `json:"botToken"`
		AllowedUserIDs []string `json:"allowedUserIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := b.SaveConfig(body.BotToken, body.AllowedUserIDs); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	b.Start()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}
