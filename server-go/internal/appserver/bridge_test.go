package appserver

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"codex-server/internal/rpc"
)

func TestBridgeHandleLineResponse(t *testing.T) {
	b := NewBridge(Config{})
	b.pendingMu.Lock()
	ch := make(chan *rpc.Response, 1)
	b.pending[1] = ch
	b.pendingMu.Unlock()

	line := []byte(`{"jsonrpc":"2.0","id":1,"result":{"data":"success"}}`)
	b.handleLine(line)

	select {
	case resp := <-ch:
		if resp == nil {
			t.Fatal("expected non-nil response")
		}
		var resMap map[string]string
		if err := json.Unmarshal(resp.Result, &resMap); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if resMap["data"] != "success" {
			t.Errorf("expected data=success, got %v", resMap["data"])
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for response")
	}
}

func TestBridgeNotificationBroadcast(t *testing.T) {
	b := NewBridge(Config{})

	var wg sync.WaitGroup
	wg.Add(1)
	var receivedMethod string

	unsub := b.SubscribeNotification(func(n rpc.Notification) {
		receivedMethod = n.Method
		wg.Done()
	})
	defer unsub()

	line := []byte(`{"jsonrpc":"2.0","method":"thread/status","params":{"state":"active"}}`)
	b.handleLine(line)

	c := make(chan struct{})
	go func() {
		wg.Wait()
		close(c)
	}()

	select {
	case <-c:
		if receivedMethod != "thread/status" {
			t.Errorf("expected method thread/status, got %s", receivedMethod)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for notification")
	}
}
