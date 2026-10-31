package dream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"
)

// The oracle: agy's own print-mode reports of the permission state, which
// start no turn, spend no quota and leave no conversation (since agy
// 1.1.12). Its fields and their meaning are what agy 1.4.9 showed in the
// 2026-10-28 sandbox runs (plan D24), unchanged in 1.4.12's capture
// (testdata/agy-1.4.12); anything else fails closed (plan D6).
var (
	oracleTimeout = 30 * time.Second
	oracleGrace   = 5 * time.Second
)

// reviewModes are the toolPermission values under which a tool call waits
// for review: request-review (agy's default, which grants only reads inside
// the workspace) and strict. always-proceed and proceed-in-sandbox approve
// tools, and a value agy does not know reads back as request-review.
var reviewModes = map[string]bool{"request-review": true, "strict": true}

// plainModes are the agentMode values that approve nothing themselves: the
// default agent and plan, which the dream's call passes. accept-edits and
// auto approve edits and commands.
var plainModes = map[string]bool{"": true, "plan": true}

// visibleHome runs the oracle in home from work and reports whether every
// tool needs review there, and why not when one does not. env is fyr's
// environment; the oracle gets childEnv's allow-list with HOME set to home.
func visibleHome(argv0, home, work string, env []string) (ok bool, why string) {
	if _, _, err := loginIn(runtime.GOOS, home); err != nil {
		return false, "no Antigravity login in " + home
	}
	if why := workAlone(home, work); why != "" {
		return false, why
	}
	env = childEnv(env, home)
	perms, err := oracle(argv0, "permissions", work, env)
	if err != nil {
		return false, err.Error()
	}
	if why := permissionsGrant(perms); why != "" {
		return false, why
	}
	conf, err := oracle(argv0, "config", work, env)
	if err != nil {
		return false, err.Error()
	}
	if why := configGrants(conf, work); why != "" {
		return false, why
	}
	hooks, err := oracle(argv0, "hooks", work, env)
	if err != nil {
		return false, err.Error()
	}
	if why := hooksAny(hooks); why != "" {
		return false, why
	}
	return true, ""
}

// hooksAny says why /hooks reports a hook, or a report of a shape fyr does
// not know, or "": a pre-tool hook can return a permission decision the
// other reports never show, so a home with any hook runs private (plan D26).
func hooksAny(data json.RawMessage) string {
	var d map[string]json.RawMessage
	var hooks []json.RawMessage
	if err := json.Unmarshal(data, &d); err != nil || len(d) != 1 || json.Unmarshal(d["hooks"], &hooks) != nil || d["hooks"][0] != '[' {
		return "the oracle /hooks: report of an unknown shape"
	}
	if len(hooks) > 0 {
		return fmt.Sprintf("%d hook(s) loaded, which can approve a tool", len(hooks))
	}
	return ""
}

// workAlone says why work would bring more than the operator's own setup
// into the session, or "": it must be empty, and no directory above it may
// hold a .git (a checkout, whose .agents/ agy loads) or a .agents other than
// home's, which is the operator's own (plan D8). agy walks the directory
// its working directory resolves to, so the walk runs over both forms.
func workAlone(home, work string) string {
	entries, err := os.ReadDir(work)
	if err != nil {
		return "dream-work unreadable: " + err.Error()
	}
	if len(entries) > 0 {
		return "dream-work is not empty (" + entries[0].Name() + ")"
	}
	homes := map[string]bool{filepath.Clean(home): true}
	if r, err := filepath.EvalSymlinks(home); err == nil {
		homes[r] = true
	}
	for _, w := range workForms(work) {
		for dir := filepath.Dir(w); ; dir = filepath.Dir(dir) {
			if exists(filepath.Join(dir, ".git")) {
				return "dream-work is inside a git checkout (" + dir + ")"
			}
			if exists(filepath.Join(dir, ".agents")) && !homes[dir] {
				return "a .agents directory above dream-work (" + dir + ")"
			}
			if dir == filepath.Dir(dir) {
				break
			}
		}
	}
	return ""
}

// workForms is work as written and, when it differs, as it resolves.
func workForms(work string) []string {
	forms := []string{filepath.Clean(work)}
	if r, err := filepath.EvalSymlinks(work); err == nil && r != forms[0] {
		forms = append(forms, r)
	}
	return forms
}

// exists reports whether path names anything, a dangling link included.
func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// oracle runs `agy -p /<name> --output-format json` and returns its data
// object. agy starts a language server: runGroup kills its group once it is
// done, at the deadline or on a signal, as for the call (BUG-169).
func oracle(argv0, name, work string, env []string) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), oracleTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv0, "-p", "/"+name, "--output-format", "json")
	cmd.Dir, cmd.Env = work, env
	var out bytes.Buffer
	cmd.Stdout = &out
	err := runGroup(cmd, oracleGrace)
	label := "the oracle /" + name
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("%s timed out after %s", label, oracleTimeout)
	}
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		return nil, fmt.Errorf("%s: %v", label, err)
	}
	data, err := oracleData(out.Bytes(), name)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", label, err)
	}
	return data, nil
}

// envelopeKeys and commandKeys are the keys of agy 1.4.9's report around
// the data, at each level (plan D24): a key added at any level is a report fyr has not seen
// and fails closed, as configKeys does inside /config (security review of
// Phase 5, finding 2).
var (
	envelopeKeys = []string{"command", "conversation_id", "duration_seconds", "num_turns", "response", "status", "usage"}
	commandKeys  = []string{"data", "name"}
)

// oracleData is the data of a report that is exactly one JSON object with
// status SUCCESS and the command asked for, each level holding only the
// keys agy 1.4.9 reported and the data only the key named for the command.
func oracleData(out []byte, name string) (json.RawMessage, error) {
	var r struct {
		Status  string `json:"status"`
		Command *struct {
			Name string          `json:"name"`
			Data json.RawMessage `json:"data"`
		} `json:"command"`
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	if err := dec.Decode(&r); err != nil {
		return nil, errors.New("output is not a report: " + err.Error())
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("output holds more than one report")
	}
	if r.Status != "SUCCESS" || r.Command == nil || r.Command.Name != name || len(r.Command.Data) == 0 || string(r.Command.Data) == "null" {
		return nil, fmt.Errorf("report of an unknown shape (status %q)", r.Status)
	}
	env, err := onlyKeys(out, envelopeKeys)
	if err == nil {
		var cmd map[string]json.RawMessage
		if cmd, err = onlyKeys(env["command"], commandKeys); err == nil {
			_, err = onlyKeys(cmd["data"], []string{name})
		}
	}
	if err != nil {
		return nil, err
	}
	return r.Command.Data, nil
}

// onlyKeys is raw's object, or why it holds a key outside allowed.
func onlyKeys(raw json.RawMessage, allowed []string) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, errors.New("report of an unknown shape: " + err.Error())
	}
	for _, k := range sortedKeys(m) {
		if !slices.Contains(allowed, k) {
			return nil, fmt.Errorf("report of an unknown shape (key %q)", k)
		}
	}
	return m, nil
}

// permissionsGrant says which rule /permissions reports that grants a
// tool, or "": an allow rule in any scope (project, shared, global), a key
// fyr does not know, or a report without the global scope fails. Deny and
// ask rules grant nothing.
func permissionsGrant(data json.RawMessage) string {
	var d struct {
		Permissions []map[string]json.RawMessage `json:"permissions"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return "the oracle /permissions: report of an unknown shape: " + err.Error()
	}
	global := false
	for _, e := range d.Permissions {
		var scope string
		if err := json.Unmarshal(e["scope"], &scope); err != nil || scope == "" {
			return "the oracle /permissions: a scope without a name"
		}
		global = global || scope == "global"
		for _, k := range sortedKeys(e) {
			if why := ruleGrants(scope, k, e[k]); why != "" {
				return why
			}
		}
	}
	if !global {
		return "the oracle /permissions: report of an unknown shape (no global scope)"
	}
	return ""
}

// ruleGrants is permissionsGrant's verdict on one key of one scope.
func ruleGrants(scope, key string, v json.RawMessage) string {
	if key == "scope" {
		return ""
	}
	var rules []string
	if key != "allow" && key != "deny" && key != "ask" {
		return fmt.Sprintf("the oracle /permissions: an unknown key %q in the %s scope", key, scope)
	}
	if err := json.Unmarshal(v, &rules); err != nil {
		return fmt.Sprintf("the oracle /permissions: %s rules of an unknown shape in the %s scope", key, scope)
	}
	if key == "allow" && len(rules) > 0 {
		return fmt.Sprintf("an allow rule in the %s permissions (%s)", scope, rules[0])
	}
	return ""
}

// oracleConfig is what /config reports that the verdict reads.
type oracleConfig struct {
	mode, agent, artifacts string
	outside                bool
	trusted                []string
	perms                  map[string][]string
}

// configKeys are the 36 settings agy 1.4.9's /config reports (plan D24): a
// key added or gone is a report fyr has not seen, which fails closed, so a
// new approval setting in an agy upgrade sends the dream private until fyr
// learns it (plan D6, D26).
var configKeys = []string{"agentMode", "allowNonWorkspaceAccess", "altScreenMode", "artifactReviewPolicy",
	"clearScrollbackOnResize", "colorScheme", "contextWindow", "copyOnSelect", "customModelsConfig",
	"disableSlashCommands", "editor", "editorMode", "enableTerminalSandbox", "gcp", "hideActiveItems",
	"historySize", "model", "modelProvider", "notifications", "permissions", "pickerGrouping",
	"queuedMessages", "runningLightSpeed", "sandboxProxy", "seasonalThemesOff", "seasonalThemesOptIn",
	"showFeedbackSurvey", "showTips", "statusLine", "title", "toolPermission", "trustedWorkspaces",
	"useG1Credits", "useTerminalCursor", "verbosity", "vimInsertFirst"}

// readConfig is /config's fields, or why the report is of a shape fyr does
// not know: a key outside configKeys or missing from it, a field of another
// type, or a permissions key other than allow, deny and ask.
func readConfig(data json.RawMessage) (oracleConfig, string) {
	var d struct {
		Config map[string]json.RawMessage `json:"config"`
	}
	var c oracleConfig
	if err := json.Unmarshal(data, &d); err != nil {
		return c, "the oracle /config: report of an unknown shape: " + err.Error()
	}
	known := map[string]bool{}
	for _, k := range configKeys {
		known[k] = true
		if _, ok := d.Config[k]; !ok {
			return c, "the oracle /config: no " + k
		}
	}
	for _, k := range sortedKeys(d.Config) {
		if !known[k] {
			return c, fmt.Sprintf("the oracle /config: an unknown key %q", k)
		}
	}
	for _, f := range []struct {
		key string
		to  any
	}{{"toolPermission", &c.mode}, {"agentMode", &c.agent}, {"artifactReviewPolicy", &c.artifacts}, {"allowNonWorkspaceAccess", &c.outside}, {"trustedWorkspaces", &c.trusted}, {"permissions", &c.perms}} {
		if json.Unmarshal(d.Config[f.key], f.to) != nil {
			return c, "the oracle /config: no " + f.key + " of the known type"
		}
	}
	for _, k := range sortedKeys(c.perms) {
		if k != "allow" && k != "deny" && k != "ask" {
			return c, fmt.Sprintf("the oracle /config: an unknown permissions key %q", k)
		}
	}
	return c, ""
}

// sortedKeys is m's keys in order, so the first reason named is stable.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// configGrants says which setting /config reports that grants a tool, or
// lets more than dream-work in, or "": the review mode, the agent mode,
// access outside the workspace, an allow rule in the settings' own
// permissions, and a trusted workspace at or above work (trust loads a
// workspace's hooks).
func configGrants(data json.RawMessage, work string) string {
	c, why := readConfig(data)
	switch {
	case why != "":
		return why
	case !reviewModes[c.mode]:
		return "tool permission " + c.mode + " approves tools without review"
	case !plainModes[c.agent]:
		return "agent mode " + c.agent + " approves tools without review"
	case c.artifacts != "asks-for-review":
		// The one value seen; another lets a plan proceed on its own.
		return "artifact review policy " + c.artifacts + " is not asks-for-review"
	case c.outside:
		return "access outside the workspace is allowed"
	case len(c.perms["allow"]) > 0:
		return "an allow rule in the settings (" + c.perms["allow"][0] + ")"
	}
	return trustCovers(c.trusted, work)
}

// trustCovers names the first trusted workspace at or above work, as written
// or as it resolves, or "".
func trustCovers(trusted []string, work string) string {
	for _, t := range trusted {
		for _, w := range workForms(work) {
			if covers(t, w) {
				return "a trusted workspace covers dream-work (" + t + ")"
			}
		}
	}
	return ""
}

// covers reports whether dir is path or above it; a dir that is not an
// absolute path is taken to cover it, toward the private branch.
func covers(dir, path string) bool {
	if !filepath.IsAbs(dir) {
		return true
	}
	dir, path = filepath.Clean(dir), filepath.Clean(path)
	return dir == path || dir == string(filepath.Separator) || strings.HasPrefix(path, dir+string(filepath.Separator))
}
