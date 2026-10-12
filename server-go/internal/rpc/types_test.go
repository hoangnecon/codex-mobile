package rpc

import (
	"encoding/json"
	"testing"
)

func TestInboundMessageDiscrimination(t *testing.T) {
	// Case 1: Response
	respJSON := `{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`
	var m1 InboundMessage
	if err := json.Unmarshal([]byte(respJSON), &m1); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if !m1.IsResponse() {
		t.Errorf("expected m1 to be response")
	}

	// Case 2: Notification
	notifJSON := `{"jsonrpc":"2.0","method":"turn/started","params":{"threadId":"abc"}}`
	var m2 InboundMessage
	if err := json.Unmarshal([]byte(notifJSON), &m2); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if !m2.IsNotification() {
		t.Errorf("expected m2 to be notification")
	}

	// Case 3: Server Request
	reqJSON := `{"jsonrpc":"2.0","id":99,"method":"request/approval","params":{"type":"exec"}}`
	var m3 InboundMessage
	if err := json.Unmarshal([]byte(reqJSON), &m3); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if !m3.IsServerRequest() {
		t.Errorf("expected m3 to be server request")
	}
}

func TestNewRequest(t *testing.T) {
	req, err := NewRequest(42, "turn/start", map[string]string{"message": "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.ID != 42 || req.Method != "turn/start" || req.JSONRPC != "2.0" {
		t.Errorf("unexpected request fields: %+v", req)
	}
}
