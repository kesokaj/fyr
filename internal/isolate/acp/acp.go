package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

// Command returns the argv flags used to start agy in Agent Client Protocol
// stdio mode with process isolation per session.
func Command() []string {
	return []string{"acp", "--stdio", "--process-per-session"}
}

// Client speaks JSON-RPC 2.0 to an `agy acp --stdio` subprocess.
type Client struct {
	r        *bufio.Reader
	w        io.Writer
	writeMu  sync.Mutex
	turnMu   sync.Mutex
	nextID   atomic.Int64
	grants   []string
	onDenied func(tool string)

	mu       sync.Mutex
	pending  map[int64]chan jsonrpcMessage
	notifyFn func(method string, params json.RawMessage)
	closed   chan struct{}
	closeErr error
}

// NewClient starts the background reader loop over r and sends requests on w.
func NewClient(r io.Reader, w io.Writer, grants []string, onDenied func(tool string)) *Client {
	c := &Client{
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
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return c.closeErr
}

func (c *Client) readLoop() {
	var readErr error
	for {
		line, err := readBoundedLine(c.r, maxACPLine)
		if len(line) > 0 && (err == nil || errors.Is(err, io.EOF)) {
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

func (c *Client) dispatchLine(line []byte) {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return
	}
	var msg jsonrpcMessage
	if err := json.Unmarshal([]byte(trimmed), &msg); err != nil {
		return
	}
	switch {
	case hasRPCID(msg.ID) && msg.Method == "":
		c.deliverResponse(msg)
	case hasRPCID(msg.ID):
		go c.handleServerRequest(msg.ID, msg.Method, msg.Params)
	case msg.Method != "":
		c.deliverNotification(msg.Method, msg.Params)
	}
}

func (c *Client) deliverResponse(msg jsonrpcMessage) {
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
}

func (c *Client) deliverNotification(method string, params json.RawMessage) {
	c.mu.Lock()
	fn := c.notifyFn
	c.mu.Unlock()
	if fn != nil {
		fn(method, params)
	}
}

func (c *Client) call(ctx context.Context, method string, params, out any) error {
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
		c.removePending(id)
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

func (c *Client) removePending(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// Initialize negotiates ACP protocol version 1 with the server.
func (c *Client) Initialize(ctx context.Context) error {
	req := map[string]any{
		"protocolVersion": 1,
		"clientInfo":      map[string]any{"name": "fyr", "version": "1.0"},
	}
	return c.call(ctx, "initialize", req, nil)
}

// NewSession creates a new ACP session in cwd and returns its sessionId.
func (c *Client) NewSession(ctx context.Context, cwd string) (string, error) {
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
func (c *Client) SetSessionModel(ctx context.Context, sessionID, model string) error {
	if model == "" {
		return nil
	}
	req := map[string]any{"sessionId": sessionID, "modelId": model}
	return c.call(ctx, "session/set_model", req, nil)
}

// SetSessionMode sets the permission mode (e.g. "accept-edits") for sessionID.
func (c *Client) SetSessionMode(ctx context.Context, sessionID, mode string) error {
	if mode == "" {
		return nil
	}
	req := map[string]any{"sessionId": sessionID, "modeId": mode}
	return c.call(ctx, "session/set_mode", req, nil)
}

// Steer sends a mid-session steering prompt to sessionID without waiting for turnMu.
func (c *Client) Steer(ctx context.Context, sessionID, text string) error {
	req := map[string]any{
		"sessionId": sessionID,
		"prompt":    []map[string]any{{"type": "text", "text": text}},
	}
	return c.call(ctx, "session/prompt", req, nil)
}

// CloseSession closes sessionID on the ACP server.
func (c *Client) CloseSession(ctx context.Context, sessionID string) error {
	return c.call(ctx, "session/close", map[string]any{"sessionId": sessionID}, nil)
}

type permParams struct {
	ToolName string       `json:"toolName"`
	Tool     string       `json:"tool"`
	Command  string       `json:"command"`
	ToolCall permToolCall `json:"toolCall"`
	Options  []permOption `json:"options"`
}

type permToolCall struct {
	Title    string          `json:"title"`
	Name     string          `json:"name"`
	Kind     string          `json:"kind"`
	RawInput json.RawMessage `json:"rawInput"`
}

type permOption struct {
	OptionID string `json:"optionId"`
	Kind     string `json:"kind"`
}

func (c *Client) handleServerRequest(id json.RawMessage, method string, params json.RawMessage) {
	if method != "session/request_permission" {
		c.sendResponse(id, nil, &jsonrpcError{Code: -32601, Message: "method not found: " + method})
		return
	}
	var p permParams
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
