package sandbox

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// ACPCommand returns the argv flags used to start agy in Agent Client Protocol
// stdio mode with process isolation per session.
func ACPCommand() []string {
	return []string{"acp", "--stdio", "--process-per-session"}
}

type jsonrpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

func hasRPCID(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null"
}

func parseRPCIntID(raw json.RawMessage) (int64, bool) {
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return n, true
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n, true
		}
	}
	return 0, false
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *jsonrpcError) Error() string {
	return fmt.Sprintf("acp error (%d): %s", e.Code, e.Message)
}

// ACPClient speaks JSON-RPC 2.0 to an `agy acp --stdio` subprocess.
type ACPClient struct {
	r        *bufio.Reader
	w        io.Writer
	writeMu  sync.Mutex
	nextID   atomic.Int64
	grants   []string
	onDenied func(tool string)

	mu       sync.Mutex
	pending  map[int64]chan jsonrpcMessage
	notifyFn func(method string, params json.RawMessage)
	closed   chan struct{}
	closeErr error
}

// NewACPClient starts the background reader loop over r and sends requests on w.
func NewACPClient(r io.Reader, w io.Writer, grants []string, onDenied func(tool string)) *ACPClient {
	c := &ACPClient{
		r:        bufio.NewReaderSize(r, 64<<10),
		w:        w,
		grants:   slices.Clone(grants),
		onDenied: onDenied,
		pending:  make(map[int64]chan jsonrpcMessage),
		closed:   make(chan struct{}),
	}
	go c.readLoop()
	return c
}

// Close stops the client and unblocks any pending calls.
func (c *ACPClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return c.closeErr
}

func (c *ACPClient) readLoop() {
	var readErr error
	for {
		line, err := c.r.ReadBytes('\n')
		if len(line) > 0 {
			c.dispatchLine(line)
		}
		if err != nil {
			readErr = err
			break
		}
	}
	c.mu.Lock()
	c.closeErr = readErr
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
}

func (c *ACPClient) dispatchLine(line []byte) {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return
	}
	var msg jsonrpcMessage
	if err := json.Unmarshal([]byte(trimmed), &msg); err != nil {
		return
	}
	if hasRPCID(msg.ID) && msg.Method == "" {
		id, ok := parseRPCIntID(msg.ID)
		if !ok {
			return
		}
		c.mu.Lock()
		ch, ok := c.pending[id]
		if ok {
			delete(c.pending, id)
		}
		c.mu.Unlock()
		if ok {
			ch <- msg
		}
		return
	}
	if hasRPCID(msg.ID) && msg.Method != "" {
		go c.handleServerRequest(msg.ID, msg.Method, msg.Params)
		return
	}
	if msg.Method != "" {
		c.mu.Lock()
		fn := c.notifyFn
		c.mu.Unlock()
		if fn != nil {
			fn(msg.Method, msg.Params)
		}
	}
}

func (c *ACPClient) call(ctx context.Context, method string, params, out any) error {
	id := c.nextID.Add(1)
	ch := make(chan jsonrpcMessage, 1)
	c.mu.Lock()
	select {
	case <-c.closed:
		c.mu.Unlock()
		return fmt.Errorf("acp %s: connection closed", method)
	default:
	}
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.sendRequest(id, method, params); err != nil {
		c.removePending(id)
		return err
	}
	select {
	case <-ctx.Done():
		c.removePending(id)
		return ctx.Err()
	case <-c.closed:
		return fmt.Errorf("acp %s: EOF waiting for response", method)
	case resp, ok := <-ch:
		if !ok {
			return fmt.Errorf("acp %s: closed waiting for response", method)
		}
		if resp.Error != nil {
			return resp.Error
		}
		if out != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, out)
		}
		return nil
	}
}

func (c *ACPClient) removePending(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *ACPClient) sendRequest(id int64, method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	rawID := json.RawMessage(strconv.FormatInt(id, 10))
	b, err := json.Marshal(jsonrpcMessage{JSONRPC: "2.0", ID: rawID, Method: method, Params: raw})
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.w.Write(append(b, '\n'))
	return err
}

func (c *ACPClient) sendResponse(id json.RawMessage, result any, rpcErr *jsonrpcError) {
	var raw json.RawMessage
	if result != nil {
		raw, _ = json.Marshal(result)
	}
	b, err := json.Marshal(jsonrpcMessage{JSONRPC: "2.0", ID: id, Result: raw, Error: rpcErr})
	if err != nil {
		return
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, _ = c.w.Write(append(b, '\n'))
}

// Initialize negotiates ACP protocol version 1 with the server.
func (c *ACPClient) Initialize(ctx context.Context) error {
	req := map[string]any{
		"protocolVersion": 1,
		"clientInfo":      map[string]any{"name": "fyr", "version": "1.0"},
	}
	return c.call(ctx, "initialize", req, nil)
}

// NewSession creates a new ACP session in cwd and returns its sessionId.
func (c *ACPClient) NewSession(ctx context.Context, cwd string) (string, error) {
	req := map[string]any{"cwd": cwd, "mcpServers": []any{}}
	var res struct {
		SessionID string `json:"sessionId"`
	}
	if err := c.call(ctx, "session/new", req, &res); err != nil {
		return "", err
	}
	if res.SessionID == "" {
		return "", errors.New("acp session/new returned empty sessionId")
	}
	return res.SessionID, nil
}

// SetSessionModel selects the model for sessionID.
func (c *ACPClient) SetSessionModel(ctx context.Context, sessionID, model string) error {
	if model == "" {
		return nil
	}
	req := map[string]any{"sessionId": sessionID, "modelId": model}
	return c.call(ctx, "session/set_model", req, nil)
}

// SetSessionMode sets the permission mode (e.g. "accept-edits") for sessionID.
func (c *ACPClient) SetSessionMode(ctx context.Context, sessionID, mode string) error {
	if mode == "" {
		return nil
	}
	req := map[string]any{"sessionId": sessionID, "modeId": mode}
	return c.call(ctx, "session/set_mode", req, nil)
}

// Steer sends a mid-session steering prompt to sessionID.
func (c *ACPClient) Steer(ctx context.Context, sessionID, text string) error {
	req := map[string]any{
		"sessionId": sessionID,
		"prompt":    []map[string]any{{"type": "text", "text": text}},
	}
	return c.call(ctx, "session/prompt", req, nil)
}

// CloseSession closes sessionID on the ACP server.
func (c *ACPClient) CloseSession(ctx context.Context, sessionID string) error {
	return c.call(ctx, "session/close", map[string]any{"sessionId": sessionID}, nil)
}

type acpPermParams struct {
	ToolName string          `json:"toolName"`
	Tool     string          `json:"tool"`
	Command  string          `json:"command"`
	ToolCall acpPermToolCall `json:"toolCall"`
	Options  []acpPermOption `json:"options"`
}

type acpPermToolCall struct {
	Title    string          `json:"title"`
	Name     string          `json:"name"`
	Kind     string          `json:"kind"`
	RawInput json.RawMessage `json:"rawInput"`
}

type acpPermOption struct {
	OptionID string `json:"optionId"`
	Kind     string `json:"kind"`
}

func (c *ACPClient) handleServerRequest(id json.RawMessage, method string, params json.RawMessage) {
	if method != "session/request_permission" {
		c.sendResponse(id, nil, &jsonrpcError{Code: -32601, Message: "method not found: " + method})
		return
	}
	var p acpPermParams
	_ = json.Unmarshal(params, &p)
	allowed, desc := checkToolGrant(c.grants, p)
	if !allowed && c.onDenied != nil {
		c.onDenied(desc)
	}
	optID := pickOptionID(p.Options, allowed)
	c.sendResponse(id, map[string]any{
		"outcome": map[string]any{"outcome": "selected", "optionId": optID},
	}, nil)
}
