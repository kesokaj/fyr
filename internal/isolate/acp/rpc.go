package acp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const maxACPLine = 16 << 20

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

func readBoundedLine(r *bufio.Reader, limit int) ([]byte, error) {
	var buf []byte
	for {
		frag, err := r.ReadSlice('\n')
		if len(buf)+len(frag) > limit {
			return nil, errors.New("acp line exceeds max size")
		}
		if len(buf) == 0 && err != bufio.ErrBufferFull {
			return frag, err
		}
		buf = append(buf, frag...)
		if err != bufio.ErrBufferFull {
			return buf, err
		}
	}
}

func (c *Client) sendRequest(id int64, method string, params any) error {
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

func (c *Client) sendResponse(id json.RawMessage, result any, rpcErr *jsonrpcError) {
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
