package guard

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/kesokaj/fyr/internal/trail"
)

const (
	askTranscriptScanBytes     = 32 << 10
	AskTranscriptTailBytes     = 512 << 10
	AskTranscriptMaxCandidates = 1024
)

func readTranscriptLatestUserPrompt(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return ""
	}
	fi, err := os.Lstat(p)
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
		return ""
	}
	f, err := trail.OpenRegular(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || !os.SameFile(fi, st) || st.Size() == 0 {
		return ""
	}
	text, truncated := readTranscriptTailUserPrompt(f, st.Size())
	if text != "" {
		return text
	}
	if truncated || st.Size() <= AskTranscriptTailBytes {
		return ""
	}
	return readTranscriptHeadUserPrompt(f)
}

func readTranscriptTailUserPrompt(f *os.File, size int64) (string, bool) {
	skipFirst := false
	if size > AskTranscriptTailBytes {
		scanStart := size - AskTranscriptTailBytes
		if _, err := f.Seek(scanStart-1, io.SeekStart); err != nil {
			return "", false
		}
		var prev [1]byte
		if _, err := io.ReadFull(f, prev[:]); err != nil {
			return "", false
		}
		skipFirst = prev[0] != '\n'
	}
	data, err := io.ReadAll(io.LimitReader(f, AskTranscriptTailBytes))
	if err != nil || len(data) == 0 {
		return "", false
	}
	if skipFirst {
		idx := bytes.IndexByte(data, '\n')
		if idx < 0 {
			return "", false
		}
		data = data[idx+1:]
	}
	return findLatestTranscriptUserPrompt(data, AskTranscriptMaxCandidates)
}

func readTranscriptHeadUserPrompt(f *os.File) string {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(f, askTranscriptScanBytes))
	if err != nil || len(data) == 0 {
		return ""
	}
	text, _ := findLatestTranscriptUserPrompt(data, AskTranscriptMaxCandidates)
	return text
}

func findLatestTranscriptUserPrompt(data []byte, maxCandidates int) (string, bool) {
	seen := 0
	end := len(data)
	for end > 0 {
		start := bytes.LastIndexByte(data[:end], '\n') + 1
		raw := bytes.TrimSpace(data[start:end])
		end = start - 1
		if len(raw) == 0 || raw[0] != '{' || !mayContainTranscriptUserPrompt(raw) {
			continue
		}
		if seen++; seen > maxCandidates {
			return "", true
		}
		if text := extractTranscriptUserText(string(raw)); text != "" {
			return text, false
		}
	}
	return "", false
}

var transcriptUserMarkers = []string{
	`"user"`,
	`"human"`,
	`"user_input"`,
	`"userinput"`,
	`"lastuserinput"`,
	`"last_user_input"`,
	`"prompt"`,
}

func mayContainTranscriptUserPrompt(line []byte) bool {
	low := strings.ToLower(string(line))
	for _, marker := range transcriptUserMarkers {
		if strings.Contains(low, marker) {
			return true
		}
	}
	return false
}

func extractTranscriptUserText(line string) string {
	var row map[string]any
	if json.Unmarshal([]byte(line), &row) != nil {
		return ""
	}
	if hasExplicitNonUserRole(row) {
		return ""
	}
	if s := ExtractStringArg(row, "userInput", "user_input", "lastUserInput", "last_user_input", "prompt"); s != "" {
		return s
	}
	if isUserTranscriptRole(row) {
		if s := extractContentText(row["content"]); s != "" {
			return s
		}
		if s := ExtractStringArg(row, "text", "body"); s != "" {
			return s
		}
	}
	if msg, ok := row["message"].(map[string]any); ok && isUserTranscriptRole(msg) {
		return extractContentText(msg["content"])
	}
	return ""
}

func isUserTranscriptRoleValue(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "user", "user_input", "human":
		return true
	default:
		return false
	}
}

func hasExplicitNonUserRole(row map[string]any) bool {
	for _, k := range []string{"type", "role", "author"} {
		if s, ok := row[k].(string); ok {
			if strings.TrimSpace(s) != "" && !isUserTranscriptRoleValue(s) {
				return true
			}
		}
	}
	return false
}

func isUserTranscriptRole(row map[string]any) bool {
	if hasExplicitNonUserRole(row) {
		return false
	}
	for _, k := range []string{"type", "role", "author"} {
		if s, ok := row[k].(string); ok && isUserTranscriptRoleValue(s) {
			return true
		}
	}
	return false
}

func extractContentText(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	arr, ok := v.([]any)
	if !ok {
		return ""
	}
	var out strings.Builder
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if s := ExtractStringArg(m, "text", "content"); s != "" {
			if out.Len() > 0 {
				out.WriteByte('\n')
			}
			out.WriteString(s)
		}
	}
	return strings.TrimSpace(out.String())
}

// ExtractStringArg returns the first non-empty trimmed string value for keys in args.
func ExtractStringArg(args map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := args[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

// ExtractStringSliceArg returns the first non-empty []string slice extracted from JSON-decoded []any under keys.
func ExtractStringSliceArg(m map[string]any, keys ...string) []string {
	for _, k := range keys {
		arr, ok := m[k].([]any)
		if !ok {
			continue
		}
		var out []string
		for _, elem := range arr {
			if s, ok := elem.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}
