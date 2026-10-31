package eval

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/brain"
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
		"| `ind` |  | p |  | true |  | named | 1 | indicator |  | grep_search |\n"
	rows, err := ParseSuite(body)
	if err != nil {
		t.Fatal(err)
	}
	want := []Row{
		defaulted(Row{ID: "empty", Prompt: "p", Check: "true", Grants: "named"}),
		{ID: "set", Prompt: "p", Check: "true", Grants: "named", Version: 3, Kind: KindAdversarial, Cluster: "inject", Expect: "Read,fyr record get,view_file"},
		{ID: "reg", Prompt: "p", Check: "true", Grants: "named", Version: 12, Kind: KindRegression, Cluster: "catalog"},
		{ID: "ind", Prompt: "p", Check: "true", Grants: "named", Version: 1, Kind: KindIndicator, Cluster: "ind", Expect: "grep_search"},
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
		"an unknown kind":                   {long(" | safety |  | "), "kind \"safety\": want capability, regression, adversarial or indicator"},
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

// TestSuiteHarness parses this checkout's own GUIDE-eval from its trail
// export: one row per row of run.sh's case list except resume (D25), with
// run.sh's agent and wide flags.
func TestSuiteHarness(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	recs, ok, err := brain.ReadTrail(root)
	if err != nil || !ok {
		t.Fatalf("reading the trail: ok %v, %v", ok, err)
	}
	var body string
	for _, r := range recs {
		if r.ID == "GUIDE-eval" {
			body = r.Body
		}
	}
	if body == "" {
		t.Fatal("no GUIDE-eval record in .agents/trail.jsonl")
	}
	rows, err := ParseSuite(body)
	if err != nil {
		t.Fatal(err)
	}
	// run.sh's case list (run.sh:66-204): row → agent, wide.
	want := map[string]struct {
		agent string
		wide  bool
	}{
		"scaffold": {"", true}, "dispatch-bugs": {"bugs", false}, "dispatch-changelog": {"changelog", false},
		"dispatch-documentation": {"documentation", false}, "agents-security": {"security", false},
		"agent-reviewer": {"reviewer", false}, "agent-explorer": {"explorer", false}, "agent-verifier": {"verifier", false},
		"agent-dreamer": {"dreamer", false}, "inject-bugs": {"bugs", false}, "inject-changelog": {"changelog", false},
		"inject-documentation": {"documentation", false}, "lane-reviewer": {"reviewer", false}, "lane-bugs": {"bugs", false},
		"lane-documentation": {"documentation", false}, "project-memory": {"", false},
		"feature": {"", false}, "bug": {"", true}, "review": {"", true}, "review-docs-check": {"", true},
		"requirements-check": {"", false}, "preflight": {"", true}, "handoff": {"", true}, "metrics-feed": {"", true},
		"retro": {"", true}, "operate": {"", false}, "documentation": {"", true}, "discover-skills": {"", false},
		"gcloud-auth": {"", false},
		// Ported from GUIDE-skill-matrix for the v3.0.0 gate's per-skill runs.
		"api-auth": {"", false}, "bash": {"", false}, "cloud-run": {"", false}, "data": {"", false},
		"distill": {"", true}, "docker": {"", false}, "dream": {"", true}, "explain-project": {"", false},
		"feedback": {"", false}, "frontend": {"", false}, "gke": {"", false}, "golang": {"", false},
		"harness-audit-elsewhere": {"", false}, "research": {"", false}, "review-standards": {"", false},
		"spec": {"", false}, "terraform": {"", false}, "testing-strategy": {"", false}, "typescript": {"", false},
		"catalog-no-leak": {"", false}, "catalog-route-1": {"", false}, "catalog-route-2": {"", false},
		"catalog-route-3": {"", false}, "catalog-route-4": {"", false}, "catalog-route-5": {"", false},
		"catalog-route-6": {"", false}, "catalog-precedence": {"", false},
		"rule-code-modularity": {"", true}, "build-cli": {"", true}, "build-api": {"", true}, "build-fullstack": {"", true},
		// The sandbox form of GUIDE-testing § 5's context check (the v3.1.0 gate).
		"smoke": {"", false},
		// The work skill's model: lines (PLAN-2026-10-29-workers Phase 8b, D128).
		"work-kickoff-model": {"", false},
		// The work skill's steps 6-11 after one wave (D138 item 6, D139 item 4).
		"work-squash-r2": {"", true},
	}
	// The kinds (PLAN.md P9): every other row is capability, and every row
	// is version 1, its own cluster, with no expect, until its author says.
	kinds := map[string]string{"smoke": KindRegression, "catalog-no-leak": KindRegression, "catalog-route-1": KindRegression,
		"catalog-route-2": KindRegression, "catalog-route-3": KindRegression, "catalog-route-4": KindRegression,
		"catalog-route-5": KindRegression, "catalog-route-6": KindRegression, "catalog-precedence": KindRegression,
		"agents-security": KindAdversarial, "agent-reviewer": KindAdversarial, "inject-bugs": KindAdversarial,
		"inject-changelog": KindAdversarial, "inject-documentation": KindAdversarial, "lane-reviewer": KindAdversarial,
		"lane-bugs": KindAdversarial, "lane-documentation": KindAdversarial}
	got := map[string]Row{}
	for _, r := range rows {
		got[r.ID] = r
		kind := kinds[r.ID]
		if kind == "" {
			kind = KindCapability
		}
		if r.Kind != kind || r.Version != 1 || r.Cluster != r.ID || r.Expect != "" {
			t.Errorf("row %s: kind %q version %d cluster %q expect %q, want kind %q version 1, its own cluster, no expect", r.ID, r.Kind, r.Version, r.Cluster, r.Expect, kind)
		}
	}
	for id, w := range want {
		r, ok := got[id]
		if !ok {
			t.Errorf("row %s is missing", id)
			continue
		}
		if _, wide := r.Wide(); r.Agent != w.agent || wide != w.wide {
			t.Errorf("row %s: agent %q wide %v, want agent %q wide %v", id, r.Agent, wide, w.agent, w.wide)
		}
		// The kit's shell-expanded names are gone (D21).
		for _, s := range []string{r.Setup, r.Prompt, r.Prompt2, r.Check} {
			if strings.Contains(s, "KIT_") || strings.Contains(s, "$(date") {
				t.Errorf("row %s still names a kit variable or a shell date: %q", id, s)
			}
		}
	}
	if _, ok := got["resume"]; ok {
		t.Error("the resume row is struck (D25)")
	}
	if len(rows) != len(want) {
		t.Errorf("%d rows, want %d", len(rows), len(want))
	}
	// Each check and setup is bash that parses.
	for _, r := range rows {
		for what, s := range map[string]string{"setup": r.Setup, "check": r.Check} {
			if s == "" {
				continue
			}
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
