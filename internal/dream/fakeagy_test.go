package dream

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// agyLogin is a real HOME holding an Antigravity login, as agyHome copies it.
func agyLogin(t *testing.T) string {
	t.Helper()
	real := t.TempDir()
	write(t, filepath.Join(real, ".gemini", "antigravity-cli", "antigravity-oauth-token"), "token", fresh)
	write(t, filepath.Join(real, ".gemini", "antigravity-cli", "settings.json"), `{"toolPermission":"always-proceed"}`, fresh)
	t.Setenv("HOME", real)
	return real
}

// fakeAgy is fakeCLI for agy with a login to copy, answering response as
// the result event of a stream-json run.
func fakeAgy(t *testing.T, response string, code int) (rec string) {
	t.Helper()
	agyLogin(t)
	return fakeCLI(t, "agy", agyResult(response, false), code)
}

func agyResult(response string, denied bool) string {
	res := map[string]any{"status": "SUCCESS", "response": response}
	if denied {
		res["denied_actions"] = []map[string]string{{"action": "command"}}
	}
	b, _ := json.Marshal(map[string]any{"event": "result", "result": res})
	return `{"event":"init"}` + "\n" + string(b) + "\n"
}
