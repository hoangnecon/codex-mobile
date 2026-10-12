package terminal

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
)

type Session struct {
	ID         string
	cmd        *exec.Cmd
	ptmx       *os.File
	buffer     bytes.Buffer
	bufferMu   sync.Mutex
	subscribers map[chan []byte]struct{}
	subMu      sync.RWMutex
	closed     bool
	createdAt  time.Time
}

type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
	}
}

func getShell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	if _, err := os.Stat("/bin/zsh"); err == nil {
		return "/bin/zsh"
	}
	if _, err := os.Stat("/bin/bash"); err == nil {
		return "/bin/bash"
	}
	return "/bin/sh"
}

func (m *Manager) GetOrCreate(id string, cwd string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if s, ok := m.sessions[id]; ok && !s.closed {
		return s, nil
	}

	shell := getShell()
	cmd := exec.Command(shell)
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to start pty: %w", err)
	}

	s := &Session{
		ID:          id,
		cmd:         cmd,
		ptmx:        ptmx,
		subscribers: make(map[chan []byte]struct{}),
		createdAt:   time.Now(),
	}
	m.sessions[id] = s

	// Read loop
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])

				// Store in circular history buffer (max 256KB)
				s.bufferMu.Lock()
				if s.buffer.Len() > 256*1024 {
					s.buffer.Reset()
				}
				s.buffer.Write(chunk)
				s.bufferMu.Unlock()

				// Broadcast to subscribers
				s.subMu.RLock()
				for ch := range s.subscribers {
					select {
					case ch <- chunk:
					default:
					}
				}
				s.subMu.RUnlock()
			}
			if err != nil {
				break
			}
		}

		s.subMu.Lock()
		s.closed = true
		for ch := range s.subscribers {
			close(ch)
			delete(s.subscribers, ch)
		}
		s.subMu.Unlock()
		_ = ptmx.Close()
	}()

	return s, nil
}

func (m *Manager) WriteInput(id string, data []byte) error {
	m.mu.RLock()
	s, ok := m.sessions[id]
	m.mu.RUnlock()

	if !ok || s.closed {
		return errors.New("terminal session not active")
	}
	_, err := s.ptmx.Write(data)
	return err
}

func (m *Manager) Resize(id string, cols, rows uint16) error {
	m.mu.RLock()
	s, ok := m.sessions[id]
	m.mu.RUnlock()

	if !ok || s.closed {
		return errors.New("terminal session not active")
	}
	ws := &pty.Winsize{
		Cols: cols,
		Rows: rows,
	}
	return pty.Setsize(s.ptmx, ws)
}

func (m *Manager) GetSnapshot(id string) []byte {
	m.mu.RLock()
	s, ok := m.sessions[id]
	m.mu.RUnlock()

	if !ok {
		return nil
	}
	s.bufferMu.Lock()
	defer s.bufferMu.Unlock()
	return s.buffer.Bytes()
}

func (m *Manager) Close(id string) error {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()

	if !ok || s.closed {
		return nil
	}
	s.closed = true
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	if s.ptmx != nil {
		_ = s.ptmx.Close()
	}
	return nil
}

func (m *Manager) StreamOutput(id string, w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	m.mu.RLock()
	s, exists := m.sessions[id]
	m.mu.RUnlock()

	if !exists || s.closed {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Send initial snapshot
	snapshot := m.GetSnapshot(id)
	if len(snapshot) > 0 {
		_, _ = w.Write(snapshot)
		flusher.Flush()
	}

	ch := make(chan []byte, 64)
	s.subMu.Lock()
	s.subscribers[ch] = struct{}{}
	s.subMu.Unlock()

	defer func() {
		s.subMu.Lock()
		delete(s.subscribers, ch)
		s.subMu.Unlock()
	}()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case chunk, ok := <-ch:
			if !ok {
				return
			}
			_, _ = w.Write(chunk)
			flusher.Flush()
		}
	}
}
