package dream

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var skillName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// response is the one JSON object the CLI is asked for.
type response struct {
	Proposals []proposal      `json:"proposals"`
	Skills    []draft         `json:"skills"`
	Ratings   []sessionRating `json:"ratings"`
}

type proposal struct {
	Title, Kind, Target, File, Text, Evidence string
}

type draft struct {
	Name, Description, Body string
}

type sessionRating struct {
	ConversationID  string `json:"conversation_id"`
	SessionID       string `json:"session_id"`
	OutcomeScore    int    `json:"outcome_score"`
	TokenEfficiency int    `json:"token_efficiency"`
	PlanAlignment   string `json:"plan_alignment"`
	SkillAction     string `json:"skill_action"`
	TargetSkill     string `json:"target_skill"`
	Summary         string `json:"summary"`
}

func (r sessionRating) id() string {
	if strings.TrimSpace(r.ConversationID) != "" {
		return r.ConversationID
	}
	return r.SessionID
}

// answer is the model's text: Antigravity's stream-json ends with a result
// event that carries it as "response". A result that names denied actions is
// no answer.
func answer(stdout string) (string, error) {
	sc := bufio.NewScanner(strings.NewReader(stdout))
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var ev struct {
			Event      string `json:"event"`
			StepUpdate struct {
				StepType string `json:"step_type"`
			} `json:"step_update"`
			Result struct {
				Status        string            `json:"status"`
				Response      string            `json:"response"`
				DeniedActions []json.RawMessage `json:"denied_actions"`
			} `json:"result"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		// A tool step, run or denied, means the answer was not tool-free:
		// no answer (security review of the release range). The step may
		// already have run: plan mode writes its plan file (ADR-089).
		if ev.Event == "step_update" && ev.StepUpdate.StepType == "tool" {
			return "", errors.New("CLI ran a tool step")
		}
		if ev.Event != "result" {
			continue
		}
		if len(ev.Result.DeniedActions) > 0 {
			return "", fmt.Errorf("CLI result denied %d action(s)", len(ev.Result.DeniedActions))
		}
		if ev.Result.Status != "SUCCESS" {
			return "", fmt.Errorf("CLI result status %q", ev.Result.Status)
		}
		return ev.Result.Response, nil
	}
	if err := sc.Err(); err != nil {
		// A line over the buffer stopped the scan silently and the failure
		// read "invalid output" (audit 2026-09-30, SD11).
		return "", fmt.Errorf("output line too long or unreadable: %w", err)
	}
	return "", nil
}

// parse takes the first {...} in out that decodes as the response, falling back
// to an empty valid JSON object {} with no error.
func parse(out string) (response, bool) {
	sawEmpty := false
	for i := strings.IndexByte(out, '{'); i >= 0; {
		if r, hasFields, emptyObj, ok := decodeCandidate(out[i:]); ok {
			if hasFields {
				return r, true
			}
			if emptyObj {
				sawEmpty = true
			}
		}
		next := strings.IndexByte(out[i+1:], '{')
		if next < 0 {
			break
		}
		i += next + 1
	}
	if sawEmpty {
		return response{}, true
	}
	return response{}, false
}

func decodeCandidate(s string) (r response, hasFields, emptyObj, ok bool) {
	var raw map[string]json.RawMessage
	if json.NewDecoder(strings.NewReader(s)).Decode(&raw) != nil || raw == nil {
		return response{}, false, false, false
	}
	if hasJSONError(raw) {
		return response{}, false, false, false
	}
	// Unknown keys are ignored: one extra key from a chatty model made
	// the answer invalid and the project retried every run with the same
	// prompt (audit 2026-09-30, SD5). The fields used are validated below.
	if json.NewDecoder(strings.NewReader(s)).Decode(&r) != nil {
		return response{}, false, false, false
	}
	hasFields = r.Proposals != nil || r.Skills != nil || r.Ratings != nil
	return r, hasFields, len(raw) == 0, true
}

func hasJSONError(raw map[string]json.RawMessage) bool {
	v, ok := raw["error"]
	if !ok {
		return false
	}
	t := strings.TrimSpace(string(v))
	return t != "" && t != "null" && t != "false" && t != `""`
}
