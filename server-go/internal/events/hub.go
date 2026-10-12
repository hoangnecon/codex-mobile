package events

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"codex-server/internal/rpc"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// Allow local LAN or reverse proxies
		return true
	},
}

type wsClient struct {
	conn   *websocket.Conn
	sendCh chan []byte
}

type sseClient struct {
	sendCh chan []byte
}

// Hub coordinates realtime WebSocket and SSE notification broadcasts.
type Hub struct {
	mu         sync.RWMutex
	wsClients  map[*wsClient]struct{}
	sseClients map[*sseClient]struct{}
}

// NewHub creates a new realtime event Hub.
func NewHub() *Hub {
	return &Hub{
		wsClients:  make(map[*wsClient]struct{}),
		sseClients: make(map[*sseClient]struct{}),
	}
}

// Broadcast dispatches an incoming notification to all connected WebSocket and SSE clients.
func (h *Hub) Broadcast(notif rpc.Notification) {
	data, err := json.Marshal(notif)
	if err != nil {
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	for client := range h.wsClients {
		select {
		case client.sendCh <- data:
		default:
			// Client buffer full, drop or close
		}
	}

	for client := range h.sseClients {
		select {
		case client.sendCh <- data:
		default:
		}
	}
}

// HandleWebSocket upgrades an HTTP connection to WebSocket and streams notifications.
func (h *Hub) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[events] ws upgrade failed: %v", err)
		return
	}

	client := &wsClient{
		conn:   conn,
		sendCh: make(chan []byte, 256),
	}

	h.mu.Lock()
	h.wsClients[client] = struct{}{}
	h.mu.Unlock()

	// Send initial ready notification expected by codex UI frontend
	readyMsg := map[string]any{
		"method": "ready",
		"params": map[string]any{"ok": true},
		"atIso":  time.Now().UTC().Format(time.RFC3339Nano),
	}
	if b, err := json.Marshal(readyMsg); err == nil {
		_ = conn.WriteMessage(websocket.TextMessage, b)
	}

	defer func() {
		h.mu.Lock()
		delete(h.wsClients, client)
		h.mu.Unlock()
		_ = conn.Close()
	}()

	// Writer pump
	go func() {
		for msg := range client.sendCh {
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	}()

	// Reader pump to detect client close
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

// HandleSSE streams events over HTTP Server-Sent Events.
func (h *Hub) HandleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	client := &sseClient{
		sendCh: make(chan []byte, 128),
	}

	h.mu.Lock()
	h.sseClients[client] = struct{}{}
	h.mu.Unlock()

	// Send initial comment/ready
	_, _ = fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	defer func() {
		h.mu.Lock()
		delete(h.sseClients, client)
		h.mu.Unlock()
	}()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-client.sendCh:
			_, err := fmt.Fprintf(w, "data: %s\n\n", string(msg))
			if err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
