package dream

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// realAgy holds real output of the agy this repository last checked (PLAN.md
// item 43); fakeagy_test.go's are written by hand, so they cannot notice a
// shape a new agy changed.
var realAgy = filepath.Join("testdata", "agy-1.4.12")

// streamPins are what answer reads from a stream: the event each must
// appear as, and the key of it. A tool step's type is the guard that the
// answer was tool-free, so a renamed type would pass silently.
var streamPins = []struct{ event, key string }{
	{"step_update", "step_update.step_type"},
	{"result", "result.status"},
	{"result", "result.response"},
}

// missingInStream names each event or key of streamPins the stream lacks.
func missingInStream(stream string) []string {
	seen := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(stream))
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var ev map[string]any
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		event, _ := ev["event"].(string)
		seen["event "+event] = true
		body, _ := ev[event].(map[string]any)
		for k := range body {
			seen[event+"."+k] = true
		}
	}
	var missing []string
	for _, p := range streamPins {
		if !seen["event "+p.event] && !slices.Contains(missing, "event "+p.event) {
			missing = append(missing, "event "+p.event)
		}
		if seen["event "+p.event] && !seen[p.key] {
			missing = append(missing, "key "+p.key)
		}
	}
	return missing
}

func realOutput(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(realAgy, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestRealAgyShapes runs the code that reads agy over its real output: the
// oracle's three reports grant nothing and pass, and a turn's answer is
// read, or refused when a tool ran.
func TestRealAgyShapes(t *testing.T) {
	work := t.TempDir()
	reads := map[string]func(json.RawMessage) string{
		"permissions": permissionsGrant,
		"config":      func(d json.RawMessage) string { return configGrants(d, work) },
		"hooks":       hooksAny,
	}
	for name, read := range reads {
		data, err := oracleData([]byte(realOutput(t, name+".json")), name)
		if err != nil {
			t.Fatalf("/%s: %v", name, err)
		}
		if why := read(data); why != "" {
			t.Errorf("/%s: %s", name, why)
		}
	}
	for _, name := range []string{"turn.jsonl", "tool.jsonl"} {
		if missing := missingInStream(realOutput(t, name)); len(missing) > 0 {
			t.Errorf("%s lacks %s", name, strings.Join(missing, ", "))
		}
	}
	got, err := answer(realOutput(t, "turn.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := parse(got); !ok || r.Proposals == nil {
		t.Errorf("turn.jsonl's answer %q is no response", got)
	}
	got, err = answer(realOutput(t, "tool.jsonl"))
	if err != nil || got != "" {
		t.Errorf("a turn that ran a tool must give no answer: %q, %v", got, err)
	}
}

// TestRealAgyShapesNameTheLoss proves the pins bite: each pinned shape taken
// out of the real stream is reported by name, and each key taken out of the
// real /config is refused by name.
func TestRealAgyShapesNameTheLoss(t *testing.T) {
	stream := realOutput(t, "turn.jsonl")
	if m := missingInStream(stream); len(m) > 0 {
		t.Fatalf("the unmutated stream lacks %v", m)
	}
	cases := []struct{ old, new, want string }{
		{`"step_type"`, `"stepType"`, "key step_update.step_type"},
		{`"status"`, `"state_"`, "key result.status"},
		{`"response"`, `"text"`, "key result.response"},
		{`"event":"result"`, `"event":"final"`, "event result"},
		{`"event":"step_update"`, `"event":"step"`, "event step_update"},
	}
	for _, c := range cases {
		mutated := strings.ReplaceAll(stream, c.old, c.new)
		if m := missingInStream(mutated); !slices.Contains(m, c.want) {
			t.Errorf("%s → %s: reported %v, want %q", c.old, c.new, m, c.want)
		}
	}
	data, err := oracleData([]byte(realOutput(t, "config.json")), "config")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range configKeys {
		var d struct {
			Config map[string]json.RawMessage `json:"config"`
		}
		if err := json.Unmarshal(data, &d); err != nil {
			t.Fatal(err)
		}
		delete(d.Config, key)
		b, _ := json.Marshal(d)
		if _, why := readConfig(b); !strings.Contains(why, "no "+key) {
			t.Errorf("/config without %s: %q", key, why)
		}
	}
}

// TestRealAgyArgv pins that the call the capture ran is the dream's own.
func TestRealAgyArgv(t *testing.T) {
	const captured = `agy --input-format stream-json --output-format stream-json --mode plan -p ""`
	if got := fmt.Sprintf("%s %q", strings.Join(command()[:len(command())-1], " "), command()[len(command())-1]); got != captured {
		t.Errorf("the dream's call is %s; the capture ran %s: capture again", got, captured)
	}
	if !strings.Contains(realOutput(t, "README.md"), captured) {
		t.Errorf("README.md does not name %s", captured)
	}
}
