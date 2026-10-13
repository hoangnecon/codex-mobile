package appserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"codex-server/internal/rpc"
)

// NotificationListener is invoked whenever a notification is received from app-server.
type NotificationListener func(notification rpc.Notification)

// ServerRequestListener is invoked when app-server sends a request to the client (e.g. approval).
type ServerRequestListener func(request rpc.InboundMessage) (*rpc.Response, error)

// Config holds configuration for launching codex app-server.
type Config struct {
	CodexPath string
	Args      []string
	Env       map[string]string
	WorkingDir string
}

// Bridge manages the lifecycle of the codex app-server child process and stdio JSON-RPC.
type Bridge struct {
	config Config

	mu            sync.RWMutex
	cmd           *exec.Cmd
	stdin         io.WriteCloser
	stdinMu       sync.Mutex
	running       bool
	stopping      bool
	stopCh        chan struct{}

	nextID        atomic.Int64
	pendingMu     sync.Mutex
	pending       map[int64]chan *rpc.Response

	initMu        sync.Mutex
	initialized   bool

	listenersMu   sync.RWMutex
	listeners     []NotificationListener

	requestListener ServerRequestListener
}

// NewBridge creates a new Bridge instance.
func NewBridge(cfg Config) *Bridge {
	if cfg.CodexPath == "" {
		if path, err := exec.LookPath("codex"); err == nil {
			cfg.CodexPath = path
		} else {
			cfg.CodexPath = "codex"
		}
	}
	if len(cfg.Args) == 0 {
		cfg.Args = []string{"app-server", "--listen", "stdio://"}
	}

	return &Bridge{
		config:    cfg,
		pending:   make(map[int64]chan *rpc.Response),
		listeners: make([]NotificationListener, 0),
	}
}

// SubscribeNotification adds a callback for all incoming notifications.
func (b *Bridge) SubscribeNotification(fn NotificationListener) func() {
	b.listenersMu.Lock()
	b.listeners = append(b.listeners, fn)
	idx := len(b.listeners) - 1
	b.listenersMu.Unlock()

	return func() {
		b.listenersMu.Lock()
		defer b.listenersMu.Unlock()
		if idx < len(b.listeners) {
			b.listeners = append(b.listeners[:idx], b.listeners[idx+1:]...)
		}
	}
}

// SetServerRequestListener sets the handler for server-to-client requests.
func (b *Bridge) SetServerRequestListener(fn ServerRequestListener) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.requestListener = fn
}

// Start launches the codex app-server child process.
func (b *Bridge) Start() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.running {
		return nil
	}

	cmd := exec.Command(b.config.CodexPath, b.config.Args...)
	if b.config.WorkingDir != "" {
		cmd.Dir = b.config.WorkingDir
	}

	// Setup environment
	env := os.Environ()
	for k, v := range b.config.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe error: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("stdout pipe error: %w", err)
	}

	// Discard or capture stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return fmt.Errorf("start codex app-server error: %w", err)
	}

	b.cmd = cmd
	b.stdin = stdin
	b.running = true
	b.stopping = false
	b.stopCh = make(chan struct{})

	go b.readLoop(stdout)
	go b.waitLoop()

	return nil
}

func (b *Bridge) waitLoop() {
	if b.cmd == nil {
		return
	}
	err := b.cmd.Wait()

	b.mu.Lock()
	wasRunning := b.running
	wasStopping := b.stopping
	b.running = false
	b.cmd = nil
	b.stdin = nil
	b.initialized = false
	b.mu.Unlock()

	if wasRunning && !wasStopping {
		log.Printf("[appserver] codex app-server process exited unexpectedly: %v", err)
	}

	// Reject all pending requests
	b.pendingMu.Lock()
	for id, ch := range b.pending {
		ch <- &rpc.Response{
			JSONRPC: rpc.JSONRPCVersion,
			ID:      id,
			Error: &rpc.RPCError{
				Code:    -32000,
				Message: "codex app-server process exited",
			},
		}
		close(ch)
		delete(b.pending, id)
	}
	b.pendingMu.Unlock()
}

func (b *Bridge) readLoop(r io.Reader) {
	reader := bufio.NewReaderSize(r, 1024*1024)

	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := bytes.TrimRight(line, "\r\n")
			if len(trimmed) > 0 {
				b.handleLine(trimmed)
			}
		}

		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.Printf("[appserver] stdout reader error: %v", err)
			}
			break
		}
	}
}

func (b *Bridge) handleLine(line []byte) {
	if len(line) < 500 {
		log.Printf("[appserver] inbound line: %s", string(line))
	} else {
		log.Printf("[appserver] inbound line (len=%d): %s...", len(line), string(line[:200]))
	}
	var msg rpc.InboundMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		log.Printf("[appserver] unmarshal error: %v", err)
		return
	}

	// Case 1: Inbound response to one of our requests
	if msg.IsResponse() {
		idVal, ok := toInt64(msg.ID)
		log.Printf("[appserver] response matched: id=%v (ok=%v, idVal=%d)", msg.ID, ok, idVal)
		if ok {
			b.pendingMu.Lock()
			ch, exists := b.pending[idVal]
			if exists {
				delete(b.pending, idVal)
			}
			b.pendingMu.Unlock()

			if exists && ch != nil {
				ch <- &rpc.Response{
					JSONRPC: msg.JSONRPC,
					ID:      msg.ID,
					Result:  msg.Result,
					Error:   msg.Error,
				}
				close(ch)
			}
		}
		return
	}

	// Case 2: Inbound notification
	if msg.IsNotification() {
		notif := rpc.NewNotification(msg.Method, msg.Params)
		b.listenersMu.RLock()
		listeners := make([]NotificationListener, len(b.listeners))
		copy(listeners, b.listeners)
		b.listenersMu.RUnlock()

		for _, fn := range listeners {
			go fn(notif)
		}
		return
	}

	// Case 3: Server request (app-server requesting client approval or input)
	if msg.IsServerRequest() {
		b.mu.RLock()
		handler := b.requestListener
		b.mu.RUnlock()

		if handler != nil {
			go func() {
				resp, err := handler(msg)
				if err != nil {
					_ = b.SendRawResponse(&rpc.Response{
						JSONRPC: rpc.JSONRPCVersion,
						ID:      msg.ID,
						Error: &rpc.RPCError{
							Code:    -32000,
							Message: err.Error(),
						},
					})
					return
				}
				if resp != nil {
					_ = b.SendRawResponse(resp)
				}
			}()
		}
		return
	}
}

func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	default:
		return 0, false
	}
}

// EnsureInitialized performs the initialize and initialized handshake with codex app-server.
func (b *Bridge) EnsureInitialized(ctx context.Context) error {
	b.initMu.Lock()
	defer b.initMu.Unlock()

	if b.initialized {
		return nil
	}

	initParams := map[string]any{
		"clientInfo": map[string]any{
			"name":    "codex-web-local",
			"version": "0.1.0",
		},
		"capabilities": map[string]any{
			"experimentalApi": true,
		},
	}

	resp, err := b.callInternal(ctx, "initialize", initParams)
	if err != nil {
		return fmt.Errorf("initialize call failed: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("initialize returned error: %s", resp.Error.Message)
	}

	// Send initialized notification
	notif := map[string]any{
		"jsonrpc": rpc.JSONRPCVersion,
		"method":  "initialized",
	}
	data, _ := json.Marshal(notif)
	data = append(data, '\n')

	b.stdinMu.Lock()
	if b.stdin != nil {
		_, _ = b.stdin.Write(data)
	}
	b.stdinMu.Unlock()

	b.initialized = true
	return nil
}

func (b *Bridge) callInternal(ctx context.Context, method string, params any) (*rpc.Response, error) {
	id := b.nextID.Add(1)
	log.Printf("[appserver] send request: id=%d method=%s", id, method)
	req, err := rpc.NewRequest(id, method, params)
	if err != nil {
		return nil, fmt.Errorf("create request error: %w", err)
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request error: %w", err)
	}
	data = append(data, '\n')

	respCh := make(chan *rpc.Response, 1)
	b.pendingMu.Lock()
	b.pending[id] = respCh
	b.pendingMu.Unlock()

	b.stdinMu.Lock()
	if b.stdin == nil {
		b.stdinMu.Unlock()
		b.pendingMu.Lock()
		delete(b.pending, id)
		b.pendingMu.Unlock()
		return nil, errors.New("app-server stdin not available")
	}
	_, writeErr := b.stdin.Write(data)
	b.stdinMu.Unlock()

	if writeErr != nil {
		b.pendingMu.Lock()
		delete(b.pending, id)
		b.pendingMu.Unlock()
		return nil, fmt.Errorf("write to app-server failed: %w", writeErr)
	}

	select {
	case resp, ok := <-respCh:
		if !ok || resp == nil {
			return nil, errors.New("request channel closed without response")
		}
		return resp, nil
	case <-ctx.Done():
		b.pendingMu.Lock()
		delete(b.pending, id)
		b.pendingMu.Unlock()
		return nil, ctx.Err()
	}
}

// Call sends a JSON-RPC request to the app-server and blocks until response or context timeout.
func (b *Bridge) Call(ctx context.Context, method string, params any) (*rpc.Response, error) {
	b.mu.RLock()
	running := b.running
	b.mu.RUnlock()

	if !running {
		// Auto-start if not running
		if err := b.Start(); err != nil {
			return nil, fmt.Errorf("failed to start codex app-server: %w", err)
		}
	}

	if method != "initialize" {
		if err := b.EnsureInitialized(ctx); err != nil {
			return nil, fmt.Errorf("ensure initialized error: %w", err)
		}
	}

	return b.callInternal(ctx, method, params)
}

// SendRawResponse sends a raw response back to the app-server for a server-initiated request.
func (b *Bridge) SendRawResponse(resp *rpc.Response) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	b.stdinMu.Lock()
	defer b.stdinMu.Unlock()
	if b.stdin == nil {
		return errors.New("app-server stdin not available")
	}
	_, err = b.stdin.Write(data)
	return err
}

// Close stops the app-server process gracefully.
func (b *Bridge) Close() error {
	b.mu.Lock()
	b.stopping = true
	cmd := b.cmd
	stdin := b.stdin
	b.mu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}

	if cmd != nil && cmd.Process != nil {
		done := make(chan error, 1)
		go func() {
			done <- cmd.Wait()
		}()

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
		}
	}

	return nil
}
