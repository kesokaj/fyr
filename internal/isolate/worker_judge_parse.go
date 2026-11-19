package isolate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type agyDeniedAction struct {
	Action      string `json:"action"`
	DisplayName string `json:"display_name"`
}

type agyJudgeEnvelope struct {
	ConversationID   string            `json:"conversation_id"`
	Status           string            `json:"status"`
	Response         string            `json:"response"`
	Error            string            `json:"error"`
	StructuredOutput json.RawMessage   `json:"structured_output"`
	DeniedActions    []agyDeniedAction `json:"denied_actions"`
}

// ParseJudgeVerdict parses either an `agy --output-format json` envelope or a
// bare `{"verdict":"PASS"|"FAIL","reason":"..."}` JSON object strictly.
func ParseJudgeVerdict(raw string) (verdict, reason string, err error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", errors.New("empty judge response")
	}
	inner, isEnv, err := unwrapJudgeEnvelope(trimmed)
	if err != nil {
		return "", "", err
	}
	if !isEnv {
		inner = []byte(trimmed)
	}
	return decodeStrictJudgeJSON(inner)
}

func unwrapJudgeEnvelope(trimmed string) ([]byte, bool, error) {
	var env agyJudgeEnvelope
	dec := json.NewDecoder(strings.NewReader(trimmed))
	if err := dec.Decode(&env); err != nil {
		return nil, false, nil
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false, nil
	}
	so := bytes.TrimSpace(env.StructuredOutput)
	isEnv := env.Status != "" || env.ConversationID != "" || env.Error != "" || env.Response != "" || len(so) > 0 || len(env.DeniedActions) > 0
	if !isEnv {
		return nil, false, nil
	}
	if msg := strings.TrimSpace(env.Error); msg != "" {
		if env.Status != "" {
			return nil, true, fmt.Errorf("judge agy status %s: %s", env.Status, msg)
		}
		return nil, true, fmt.Errorf("judge agy error: %s", msg)
	}
	if env.Status != "" && !strings.EqualFold(env.Status, "SUCCESS") {
		return nil, true, fmt.Errorf("judge agy status %s: unsuccessful status", env.Status)
	}
	if len(so) > 0 && string(so) != "null" {
		return so, true, nil
	}
	if resp := strings.TrimSpace(env.Response); resp != "" {
		return []byte(resp), true, nil
	}
	if len(env.DeniedActions) > 0 {
		return nil, true, fmt.Errorf("judge attempted disallowed tool action(s): %s", formatDeniedActions(env.DeniedActions))
	}
	return nil, true, errors.New("empty structured_output in judge response")
}

func formatDeniedActions(actions []agyDeniedAction) string {
	names := make([]string, 0, len(actions))
	for _, a := range actions {
		name := strings.TrimSpace(a.DisplayName)
		if name == "" {
			name = strings.TrimSpace(a.Action)
		}
		if name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "unknown"
	}
	return strings.Join(names, ", ")
}

func decodeStrictJudgeJSON(raw []byte) (string, string, error) {
	var res struct {
		Verdict string `json:"verdict"`
		Reason  string `json:"reason"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&res); err != nil {
		return "", "", fmt.Errorf("invalid judge JSON: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return "", "", errors.New("unexpected trailing tokens in judge JSON")
	}
	if res.Verdict != "PASS" && res.Verdict != "FAIL" {
		return "", "", fmt.Errorf("invalid judge verdict %q", res.Verdict)
	}
	return res.Verdict, strings.TrimSpace(res.Reason), nil
}
