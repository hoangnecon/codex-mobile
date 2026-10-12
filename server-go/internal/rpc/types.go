package rpc

import (
	"encoding/json"
	"fmt"
	"time"
)

// JSONRPCVersion is the standard JSON-RPC 2.0 version string.
const JSONRPCVersion = "2.0"

// Request represents a client or server JSON-RPC request.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response represents a JSON-RPC response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// Notification represents a JSON-RPC notification (no ID).
type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	AtIso   string          `json:"atIso,omitempty"`
}

// RPCError represents a JSON-RPC error.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("RPC Error %d: %s", e.Code, e.Message)
}

// InboundMessage is used to discriminate whether an incoming line is a response or a notification or request.
type InboundMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// IsResponse returns true if the message has an ID and either Result or Error is set (or method is empty).
func (m *InboundMessage) IsResponse() bool {
	return m.ID != nil && m.Method == ""
}

// IsNotification returns true if the message has no ID and has a Method.
func (m *InboundMessage) IsNotification() bool {
	return m.ID == nil && m.Method != ""
}

// IsServerRequest returns true if the message has both an ID and a Method.
func (m *InboundMessage) IsServerRequest() bool {
	return m.ID != nil && m.Method != ""
}

// NewNotification constructs a Notification with current timestamp.
func NewNotification(method string, params json.RawMessage) Notification {
	return Notification{
		JSONRPC: JSONRPCVersion,
		Method:  method,
		Params:  params,
		AtIso:   time.Now().UTC().Format(time.RFC3339Nano),
	}
}

// NewRequest constructs a Request with jsonrpc 2.0.
func NewRequest(id any, method string, params any) (*Request, error) {
	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		rawParams = b
	}
	return &Request{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Method:  method,
		Params:  rawParams,
	}, nil
}
