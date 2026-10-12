package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Manager handles password protection and session cookies.
type Manager struct {
	mu          sync.RWMutex
	password    string
	activeTokens map[string]time.Time
}

func NewManager(password string) *Manager {
	return &Manager{
		password:     password,
		activeTokens: make(map[string]time.Time),
	}
}

func (m *Manager) IsEnabled() bool {
	return m.password != ""
}

func (m *Manager) GenerateToken() string {
	hash := sha256.Sum256([]byte(fmtToken(m.password, time.Now().UnixNano())))
	token := hex.EncodeToString(hash[:])

	m.mu.Lock()
	m.activeTokens[token] = time.Now().Add(7 * 24 * time.Hour) // 7 days
	m.mu.Unlock()

	return token
}

func fmtToken(pwd string, salt int64) string {
	return hex.EncodeToString(sha256.New().Sum([]byte(pwd))) + hex.EncodeToString([]byte(string(rune(salt))))
}

func (m *Manager) VerifyPassword(pwd string) (string, bool) {
	if pwd == m.password {
		return m.GenerateToken(), true
	}
	return "", false
}

func (m *Manager) IsAuthorized(r *http.Request) bool {
	if !m.IsEnabled() {
		return true
	}

	// 1. Check Bearer token in Authorization header
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if m.validateToken(token) {
			return true
		}
	}

	// 2. Check cookie
	cookie, err := r.Cookie("codex_session")
	if err == nil && m.validateToken(cookie.Value) {
		return true
	}

	return false
}

func (m *Manager) validateToken(token string) bool {
	m.mu.RLock()
	expiry, exists := m.activeTokens[token]
	m.mu.RUnlock()

	if !exists {
		return false
	}
	if time.Now().After(expiry) {
		m.mu.Lock()
		delete(m.activeTokens, token)
		m.mu.Unlock()
		return false
	}
	return true
}

// Middleware enforces password protection if enabled.
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.IsEnabled() {
			next.ServeHTTP(w, r)
			return
		}

		// Allow login endpoint
		if r.URL.Path == "/codex-api/auth/login" {
			next.ServeHTTP(w, r)
			return
		}

		if !m.IsAuthorized(r) {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}
