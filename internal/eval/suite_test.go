package eval

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const header = "| id | setup | prompt | prompt2 | check | agent | grants |\n|---|---|---|---|---|---|---|\n"

const longHeader = "| id | setup | prompt | prompt2 | check | agent | grants | version | kind | cluster | expect |\n|---|---|---|---|---|---|---|---|---|---|---|\n"

// defaulted is r with the four optional columns at their defaults, as a
// seven-column suite or an empty cell gives them.
func defaulted(r Row) Row {
	r.Version, r.Kind, r.Cluster = 1, KindCapability, r.ID
	return r
}

func TestSuiteGrammar(t *testing.T) {
	body := "# GUIDE-eval\n\nProse before.\n\n| a | b |\n|---|---|\n| `x` | y |\n\n" + header +
		"| `plain` |  | say hi |  | `test -s \"$OUT\"` |  | named |\n" +
		"| `piped` (added 2026-10-26) | `echo a \\| tr a b` | p \\| q | second | `grep -c x \\| grep -q 1` | reviewer | wide: the skill pipes |\n" +
		"| `noted` | `git mv a b` and more | `whole` | `a` and `b` | `true` (changed 2026-10-09: `false` was wrong) | bugs | named |\n" +
		"| `held` |  | stamp {{STAMP}} in {{CLONE}} for {{SB}} from {{FIXDIR}} |  | true |  | named |\n" +
		"\nProse after.\n"
	rows, err := ParseSuite(body)
	if err != nil {
		t.Fatal(err)
	}
	want := []Row{
		{ID: "plain", Prompt: "say hi", Check: `test -s "$OUT"`, Grants: "named"},
		{ID: "piped", Setup: "echo a | tr a b", Prompt: "p | q", Prompt2: "second", Check: "grep -c x | grep -q 1", Agent: "reviewer", Grants: "wide: the skill pipes"},
		// The note rule (D23): a code cell keeps only its leading span; a text
		// cell loses one pair of backticks only when they wrap all of it.
		{ID: "noted", Setup: "git mv a b", Prompt: "whole", Prompt2: "`a` and `b`", Check: "true", Agent: "bugs", Grants: "named"},
		{ID: "held", Prompt: "stamp {{STAMP}} in {{CLONE}} for {{SB}} from {{FIXDIR}}", Check: "true", Grants: "named"},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(rows), len(want), rows)
	}
	// A seven-column suite parses as before, the four columns defaulted
	// (ADR-085).
	for i := range want {
		if rows[i] != defaulted(want[i]) {
			t.Errorf("row %d:\n got %+v\nwant %+v", i, rows[i], defaulted(want[i]))
		}
	}
	if reason, ok := rows[1].Wide(); !ok || reason != "the skill pipes" {
		t.Errorf("Wide() = %q, %v", reason, ok)
	}
	if _, ok := rows[0].Wide(); ok {
		t.Error("a named row reads as wide")
	}
	got := Expand(rows[3].Prompt, map[string]string{"STAMP": "2026-10-26-0800", "CLONE": "/c", "SB": "/s $HOME", "FIXDIR": "/f"})
	if got != "stamp 2026-10-26-0800 in /c for /s $HOME from /f" {
		t.Errorf("Expand = %q (a value is substituted, never interpreted)", got)
	}
}

// TestSuiteGrammarLong: the long header's four cells, each empty or set
// (ADR-085).
func TestSuiteGrammarLong(t *testing.T) {
	body := "Prose.\n\n" + longHeader +
		"| `empty` |  | p |  | true |  | named |  |  |  |  |\n" +
		"| `set` |  | p |  | true |  | named | 3 | `adversarial` | inject | Read, fyr record get ,view_file |\n" +
		"| `reg` |  | p |  | true |  | named | 12 | regression | catalog |  |\n" +
		"| `ind` |  | p |  | true |  | named | 1 | indicator |  | grep_search |\n" +
		"| `bench` |  | p |  | true |  | named | 1 | benchmark |  |  |\n"
	rows, err := ParseSuite(body)
	if err != nil {
		t.Fatal(err)
	}
	want := []Row{
		defaulted(Row{ID: "empty", Prompt: "p", Check: "true", Grants: "named"}),
		{ID: "set", Prompt: "p", Check: "true", Grants: "named", Version: 3, Kind: KindAdversarial, Cluster: "inject", Expect: "Read,fyr record get,view_file"},
		{ID: "reg", Prompt: "p", Check: "true", Grants: "named", Version: 12, Kind: KindRegression, Cluster: "catalog"},
		{ID: "ind", Prompt: "p", Check: "true", Grants: "named", Version: 1, Kind: KindIndicator, Cluster: "ind", Expect: "grep_search"},
		{ID: "bench", Prompt: "p", Check: "true", Grants: "named", Version: 1, Kind: KindBenchmark, Cluster: "bench"},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(rows), len(want), rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d:\n got %+v\nwant %+v", i, rows[i], want[i])
		}
	}
	// A 64-byte id is kept, and is a valid default cluster (the identifier
	// rule's bound).
	id := strings.Repeat("a", 64)
	if rows, err := ParseSuite(longHeader + "| `" + id + "` |  | p |  | true |  | named |  |  |  |  |\n"); err != nil || rows[0].Cluster != id {
		t.Errorf("a 64-byte id: %+v, %v", rows, err)
	}
	// 120 bytes is the trial's bound, so a list of exactly 120 is kept.
	at := manyEntries(11)
	rows, err = ParseSuite(longHeader + "| `a` |  | p |  | true |  | named |  |  |  | " + at + " |\n")
	if err != nil || len(at) != 120 || rows[0].Expect != at {
		t.Errorf("a 120-byte expect (%d bytes): %+v, %v", len(at), rows, err)
	}
}

func TestSuiteRefusals(t *testing.T) {
	row := func(cells string) string { return header + cells + "\n" }
	long := func(four string) string { return longHeader + "| `a` |  | p |  | true |  | named | " + four + " |\n" }
	for name, c := range map[string]struct{ body, want string }{
		"no table":                          {"# nothing here\n", "no table with the header"},
		"no rows":                           {header, "has no rows"},
		"a missing column":                  {row("| `a` |  | p |  | true |  | "), "columns, want 7"},
		"an extra column":                   {row("| `a` |  | p |  | true |  | named | x |"), "columns, want 7"},
		"an empty prompt":                   {row("| `a` |  |  |  | true |  | named |"), "has no prompt"},
		"an empty check":                    {row("| `a` |  | p |  |  |  | named |"), "has no check"},
		"a duplicate id":                    {header + "| `a` |  | p |  | true |  | named |\n| `a` |  | q |  | true |  | named |\n", "duplicate id a (first at line 3)"},
		"a duplicate before its own faults": {header + "| `a` |  | p |  | true |  | named |\n| `a` |  |  |  | true |  | named |\n", "duplicate id a (first at line 3)"},
		"unknown grants":                    {row("| `a` |  | p |  | true |  | all |"), "want named or wide: <reason>"},
		"wide with no reason":               {row("| `a` |  | p |  | true |  | wide:  |"), "want named or wide: <reason>"},
		"a row with no id":                  {row("| a |  | p |  | true |  | named |"), "a row starts with | `id`"},
		"an id with a slash":                {row("| `../x` |  | p |  | true |  | named |"), "the id is not one backticked name"},
		"an id past 64 bytes":               {row("| `" + strings.Repeat("a", 65) + "` |  | p |  | true |  | named |"), "the id is not one backticked name"},
		"a bad agent name":                  {row("| `a` |  | p |  | true | Re viewer | named |"), "is not an agent name"},
		"a typo placeholder":                {row("| `a` |  | in {{CLONES}} |  | true |  | named |"), "unknown placeholder {{CLONES}}"},
		"a second table":                    {header + "| `a` |  | p |  | true |  | named |\n\n" + header + "| `b` |  | p |  | true |  | named |\n", "a second suite table"},
		"a short and a long table":          {header + "| `a` |  | p |  | true |  | named |\n\n" + longHeader + "| `b` |  | p |  | true |  | named |  |  |  |  |\n", "a second suite table"},
		"seven cells under the long header": {longHeader + "| `a` |  | p |  | true |  | named |\n", "7 columns, want 11"},
		"a partial header":                  {"| id | setup | prompt | prompt2 | check | agent | grants | version |\n|---|---|---|---|---|---|---|---|\n| `a` |  | p |  | true |  | named | 1 |\n", "no table with the header"},
		"version 0":                         {long("0 |  |  | "), "line 3: row a: version \"0\""},
		"a signed version":                  {long("+1 |  |  | "), "version \"+1\""},
		"a leading zero":                    {long("01 |  |  | "), "version \"01\""},
		"a version past nine digits":        {long("1000000000 |  |  | "), "version \"1000000000\""},
		"a word for a version":              {long("two |  |  | "), "want an integer from 1"},
		"an unknown kind":                   {long(" | safety |  | "), "kind \"safety\": want capability, regression, adversarial, indicator or benchmark"},
		"a kind in capitals":                {long(" | Regression |  | "), "kind \"Regression\""},
		"a cluster of two words":            {long(" |  | a b | "), "cluster \"a b\""},
		"a cluster with a slash first":      {long(" |  | /etc | "), "cluster \"/etc\""},
		"an empty expect entry":             {long(" |  |  | Read,,Grep"), "expect entry 2 is empty"},
		"a trailing comma":                  {long(" |  |  | Read,"), "expect entry 2 is empty"},
		"a repeated expect entry":           {long(" |  |  | Read, Read"), "expect entry 2 repeats Read"},
		"a flag in an expect verb":          {long(" |  |  | fyr --home"), "expect entry 1 \"fyr --home\": each word"},
		"two spaces in an expect verb":      {long(" |  |  | fyr  record"), "expect entry 1 \"fyr  record\""},
		"prose in expect":                   {long(" |  |  | read the file; then"), "expect entry 1"},
		"an expect past 120 bytes":          {long(" |  |  | " + manyEntries(13)), "over 120 bytes"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseSuite(c.body)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("want an error holding %q, got %v", c.want, err)
			}
		})
	}
}

// manyEntries is n distinct ten-byte expect entries joined by commas.
func manyEntries(n int) string {
	var e []string
	for i := range n {
		e = append(e, fmt.Sprintf("tool_%05d", i))
	}
	return strings.Join(e, ",")
}

func TestSuiteSelectRows(t *testing.T) {
	suite := []Row{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	got, err := SelectRows(suite, []string{"c", "a"})
	if err != nil || len(got) != 2 || got[0].ID != "c" || got[1].ID != "a" {
		t.Errorf("SelectRows(c, a) = %+v, %v; want c then a", got, err)
	}
	if _, err := SelectRows(suite, []string{"a", "z"}); err == nil || !strings.Contains(err.Error(), `no row "z" in GUIDE-eval`) {
		t.Errorf("an unknown row: %v", err)
	}
	if _, err := SelectRows(suite, []string{"b", "b"}); err == nil || !strings.Contains(err.Error(), "row b is named twice") {
		t.Errorf("a repeated row: %v", err)
	}
}

// TestSuiteHarness parses this checkout's tracked suite (ADR-500) and pins
// exactly its rows: id, agent, grants, kind and version, each its own cluster
// with no expect. The rows are item 4's of PLAN-2026-11-02-1806-fyr-speed,
// item 10's three work-tasks rows, item 22's work-start and work-refuse rows,
// and item 17's two reviewer-wide rows.
func TestSuiteHarness(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	rows, found, err := ReadTrackedSuite(root, false)
	if err != nil || !found {
		t.Fatalf("reading %s: found %v, %v", SuitePath, found, err)
	}
	type pin struct {
		agent string
		wide  bool
	}
	want := map[string]pin{
		"init-project": {"", true}, "feature": {"", false}, "feature-empty": {"", true}, "feature-recon": {"explorer", true}, "review": {"", true},
		"reviewer": {"reviewer", false}, "documentation": {"documentation", false},
		"reviewer-wide": {"reviewer", true}, "reviewer-wide-bug": {"reviewer", true}, "reviewer-cold": {"reviewer", false},
		"resume":  {"", false},
		"handoff": {"", true}, "work": {"", true}, "retro": {"", true}, "distill": {"", true},
		"dream": {"", true}, "spec": {"", false}, "feedback-log": {"", false},
		"preflight": {"", true}, "project-memory": {"", false},
		"work-tasks": {"", true}, "work-tasks-waves": {"", true}, "work-tasks-design": {"", true},
		"work-start": {"", true}, "work-refuse": {"", true},
	}
	if len(rows) != len(want) {
		t.Errorf("%d rows, want %d", len(rows), len(want))
	}
	for _, r := range rows {
		w, ok := want[r.ID]
		if !ok {
			t.Errorf("row %s is not pinned", r.ID)
			continue
		}
		if _, wide := r.Wide(); r.Agent != w.agent || wide != w.wide {
			t.Errorf("row %s: agent %q wide %v, want agent %q wide %v", r.ID, r.Agent, wide, w.agent, w.wide)
		}
		if r.Kind != KindCapability || r.Version != 1 || r.Cluster != r.ID || r.Expect != "" {
			t.Errorf("row %s: kind %q version %d cluster %q expect %q, want capability, version 1, its own cluster, no expect", r.ID, r.Kind, r.Version, r.Cluster, r.Expect)
		}
		// Every scenario comes from the tracked fixture, never the clone's
		// own history.
		if !strings.HasPrefix(r.Setup, "bash .agents/eval/fixture.sh ") {
			t.Errorf("row %s: setup %q does not run the fixture", r.ID, r.Setup)
		}
		for what, s := range map[string]string{"setup": r.Setup, "check": r.Check} {
			if out, err := exec.Command("bash", "-n", "-c", s).CombinedOutput(); err != nil {
				t.Errorf("row %s: the %s does not parse as bash: %v\n%s", r.ID, what, err, out)
			}
		}
	}
}

// TestNoSQLiteDriver pins D10: internal/eval links no SQLite driver.
func TestNoSQLiteDriver(t *testing.T) {
	// GOPROXY=off: a cold module cache skips rather than fetches.
	cmd := exec.Command("go", "list", "-deps", ".")
	cmd.Env = append(os.Environ(), "GOPROXY=off")
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("go list: %v", err)
	}
	if strings.Contains(string(out), "modernc.org/sqlite") {
		t.Error("internal/eval depends on modernc.org/sqlite (D10)")
	}
}

// TestEvalSuiteFile pins D15: ReadSuiteFile reads a suite in GUIDE-eval's
// format outside every checkout and refuses a file inside a checkout or a link.
func TestEvalSuiteFile(t *testing.T) {
	body := "| id | setup | prompt | prompt2 | check | agent | grants |\n|---|---|---|---|---|---|---|\n| `hold` |  | say hi |  | `true` |  | named |\n"
	outDir := t.TempDir()
	valid := filepath.Join(outDir, "holdout.md")
	if err := os.WriteFile(valid, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	rows, err := ReadSuiteFile(valid)
	if err != nil || len(rows) != 1 || rows[0].ID != "hold" {
		t.Fatalf("ReadSuiteFile(valid) = %v, %v", rows, err)
	}
	link := filepath.Join(outDir, "link.md")
	if err := os.Symlink(valid, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSuiteFile(link); err == nil {
		t.Error("ReadSuiteFile accepted a symlink")
	}
	if _, err := ReadSuiteFile("   "); err == nil || !strings.Contains(err.Error(), "empty path") {
		t.Errorf("ReadSuiteFile(empty) = %v, want empty path", err)
	}
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	inside := filepath.Join(repo, "holdout.md")
	if err := os.WriteFile(inside, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSuiteFile(inside); err == nil || !strings.Contains(err.Error(), "inside a git checkout") {
		t.Errorf("ReadSuiteFile(inside) = %v, want inside a git checkout", err)
	}
}

// namedShellProblem returns the first shell command word in script that is not
// in allowed, or "" when every command segment starts with an allowed word.
func namedShellProblem(script string, allowed map[string]bool) string {
	script = strings.ReplaceAll(script, "\\\n", " ")
	for _, line := range strings.Split(script, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, seg := range splitShellSegments(line) {
			fields := strings.Fields(seg)
			for len(fields) > 0 && strings.Contains(fields[0], "=") && !strings.HasPrefix(fields[0], "=") {
				fields = fields[1:]
			}
			if len(fields) == 0 {
				continue
			}
			word := strings.TrimLeft(fields[0], "(")
			if word == "" {
				continue
			}
			if !allowed[word] {
				return word
			}
		}
	}
	return ""
}

func splitShellSegments(line string) []string {
	var out []string
	var quote byte
	start := 0
	for i := 0; i < len(line); i++ {
		c := line[i]
		if quote != 0 {
			if c == '\\' && quote == '"' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '#':
			if i == 0 || line[i-1] == ' ' || line[i-1] == '\t' {
				out = append(out, line[start:i])
				return out
			}
		case '|', ';':
			out = append(out, line[start:i])
			if c == '|' && i+1 < len(line) && line[i+1] == '|' {
				i++
			}
			start = i + 1
		case '&':
			if i+1 < len(line) && line[i+1] == '&' {
				out = append(out, line[start:i])
				i++
				start = i + 1
			}
		}
	}
	out = append(out, line[start:])
	return out
}

// TestNamedRowSkillCommands pins BUG-515's prevention rule: namedGrants
// includes sort, every fenced bash/sh block in the skills and agents exercised
// by a named-grant GUIDE-eval row (plus init-project Step 2, which spec and
// requirements reference) uses only commands in namedGrants, and no skill
// offers a dead "when a headless run refuses" fallback.
func TestNamedRowSkillCommands(t *testing.T) {
	allowed := map[string]bool{}
	for _, g := range namedGrants {
		if cmd, ok := strings.CutPrefix(g, "command("); ok {
			allowed[strings.TrimSuffix(cmd, ")")] = true
		}
	}
	for _, must := range []string{"ls", "head", "find", "sort"} {
		if !allowed[must] {
			t.Errorf("namedGrants is missing command(%s)", must)
		}
	}
	baseAllowed := map[string]bool{"ls": true, "head": true, "find": true, "sort": true, "echo": true, "fyr": true}
	for _, bad := range []string{
		"for d in ~/.gemini/config/skills/init-project/assets .agents/skills/init-project/assets; do\n  [ -d \"$d\" ] && ASSETS=\"$d\" && break\ndone\necho \"$ASSETS\"",
		"find cmd internal -type f | ungranted",
		"while read -r f; do echo \"$f\"; done",
		"if [ -d .agents ]; then echo ok; fi",
		"/bin/rm -rf .agents/scratch/x",
	} {
		if got := namedShellProblem(bad, baseAllowed); got == "" {
			t.Errorf("namedShellProblem passed ungranted script:\n%s", bad)
		}
	}
	for _, good := range []string{
		"ls -d ~/.gemini/config/skills/init-project/assets .agents/skills/init-project/assets 2>/dev/null | head -1",
		"find cmd internal -type f | sort",
		"fyr memory add --type feedback --name no-db-mocks --description \"d\" --body-file .agents/scratch/m.md",
	} {
		if got := namedShellProblem(good, baseAllowed); got != "" {
			t.Errorf("namedShellProblem refused granted script (%s):\n%s", got, good)
		}
	}

	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	initBytes, err := os.ReadFile(filepath.Join(root, "skills", "init-project", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, afterStep2, ok := strings.Cut(string(initBytes), "## Step 2: Locate the templates")
	if !ok {
		t.Fatal("skills/init-project/SKILL.md has no Step 2 heading")
	}
	step2, _, ok := strings.Cut(afterStep2, "\n## Step 3:")
	if !ok {
		t.Fatal("skills/init-project/SKILL.md has no Step 3 heading")
	}
	targets := map[string]string{"skills/init-project/SKILL.md#Step2": step2}
	namedRowTarget := map[string]string{
		"spec":           "skills/spec/SKILL.md",
		"feature":        "skills/feature/SKILL.md",
		"feedback-log":   "skills/feedback-log/SKILL.md",
		"project-memory": "skills/project-memory/SKILL.md",
		"reviewer":       "agents/reviewer/agent.md",
		"reviewer-cold":  "agents/reviewer/agent.md",
		"resume":         "context/AGENTS.md",
		"documentation":  "agents/documentation/agent.md",
	}
	rows, _, err := ReadTrackedSuite(root, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if _, wide := r.Wide(); !wide && namedRowTarget[r.ID] == "" {
			t.Errorf("named-grant row %q is missing from namedRowTarget in TestNamedRowSkillCommands", r.ID)
		}
	}
	for _, rel := range namedRowTarget {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		targets[rel] = string(b)
	}
	for name, text := range targets {
		parts := strings.Split(text, "```")
		for i := 1; i < len(parts); i += 2 {
			tag, body, _ := strings.Cut(parts[i], "\n")
			tag = strings.TrimSpace(tag)
			if tag != "bash" && tag != "sh" {
				continue
			}
			if bad := namedShellProblem(body, allowed); bad != "" {
				t.Errorf("%s has ungranted command %q in bash block:\n%s", name, bad, strings.TrimSpace(body))
			}
		}
	}
	specText := targets["skills/spec/SKILL.md"]
	if !strings.Contains(specText, "~/.gemini/config/skills/init-project/assets/records/SPEC-TEMPLATE.md") {
		t.Error("skills/spec/SKILL.md does not name ~/.gemini/config/skills/init-project/assets/records/SPEC-TEMPLATE.md directly")
	}
	err = filepath.WalkDir(filepath.Join(root, "skills"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() && d.Name() == "catalog" {
			if err == nil {
				return filepath.SkipDir
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		low := strings.ToLower(string(b))
		if strings.Contains(low, "when a headless run refuses") || strings.Contains(low, "when headless refuses") {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s offers a dead headless refusal fallback (print mode ends the turn on refusal)", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
