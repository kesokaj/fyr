package dream

import (
	"bufio"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var skillName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// response is the one JSON object the CLI is asked for.
type response struct {
	Proposals []proposal `json:"proposals"`
	Skills    []draft    `json:"skills"`
}

type proposal struct {
	Title, Kind, Target, File, Text, Evidence string
}

type draft struct {
	Name, Description, Body string
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
		// A tool step that ran, denied or not, means the answer was not
		// tool-free: no answer (security review of the release range).
		if ev.Event == "step_update" && ev.StepUpdate.StepType == "tool" {
			return "", nil
		}
		if ev.Event != "result" {
			continue
		}
		if ev.Result.Status != "SUCCESS" || len(ev.Result.DeniedActions) > 0 {
			return "", nil
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

// parse takes the first {...} in out that decodes as the response.
func parse(out string) (response, bool) {
	for i := strings.IndexByte(out, '{'); i >= 0; {
		var r response
		// Unknown keys are ignored: one extra key from a chatty model made
		// the answer invalid and the project retried nightly with the same
		// prompt (audit 2026-09-30, SD5). The fields used are validated below.
		dec := json.NewDecoder(strings.NewReader(out[i:]))
		if dec.Decode(&r) == nil && (r.Proposals != nil || r.Skills != nil) {
			return r, true
		}
		next := strings.IndexByte(out[i+1:], '{')
		if next < 0 {
			break
		}
		i += next + 1
	}
	return response{}, false
}
