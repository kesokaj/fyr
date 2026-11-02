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

// TestSuiteHarness parses this checkout's tracked suite (ADR-500) and pins
// exactly its rows: id, agent, grants, kind and version, each its own cluster
// with no expect. The rows are item 4's of PLAN-2026-11-02-1806-fyr-speed;
// items 10 and 22 add theirs with their behaviour.
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
		"init-project": {"", true}, "feature": {"", false}, "review": {"", true},
		"reviewer": {"reviewer", false}, "documentation": {"documentation", false},
		"handoff": {"", true}, "work": {"", true}, "retro": {"", true}, "distill": {"", true},
		"dream": {"", true}, "spec": {"", false}, "feedback-log": {"", false},
		"preflight": {"", true}, "project-memory": {"", false},
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
