// Package webpreview provides a foreground-only, read-only, in-memory HTTP
// preview server bound to loopback by default behind a random 128-bit URL path
// token (ADR-528, M24). It never reads from disk, sets cookies, or runs as a
// background daemon.
package webpreview

import (
	"encoding/hex"
	"fmt"
	"io"
	"slices"
	"strings"
)

const (
	defaultCSP    = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:"
	defaultListen = "127.0.0.1:0"
	tokenBytes    = 16 // 128 bits, encoded as 32 lowercase hex characters
)

// Asset represents an immutable, pre-rendered in-memory HTTP resource.
type Asset struct {
	ContentType string
	Body        []byte
	CSP         string
}

// Options configures a foreground in-memory HTTP preview server.
type Options struct {
	Listen     string
	DefaultCSP string
	Out, Err   io.Writer
}

func generateToken(r io.Reader) (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", fmt.Errorf("webpreview: generating token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func normalizeAssets(assets map[string]Asset) map[string]Asset {
	out := make(map[string]Asset, len(assets))
	for k, a := range assets {
		canon, ok := canonicalAssetKey(k)
		if !ok {
			continue
		}
		// An explicit leading-slash key ("/x") takes precedence over bare "x"
		// regardless of Go map iteration order.
		if _, exists := out[canon]; exists && !strings.HasPrefix(k, "/") {
			continue
		}
		out[canon] = Asset{
			ContentType: a.ContentType,
			Body:        slices.Clone(a.Body),
			CSP:         a.CSP,
		}
	}
	return out
}

func canonicalAssetKey(k string) (string, bool) {
	if k == "" || k == "/" {
		return "/", true
	}
	trimmed := strings.TrimPrefix(k, "/")
	if strings.Contains(trimmed, "\\") || strings.ContainsRune(trimmed, 0) {
		return "", false
	}
	for _, seg := range strings.Split(trimmed, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", false
		}
	}
	return "/" + trimmed, true
}
