package readledger

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/sqliteutil"
)

type testSQLiteStore struct {
	db *sql.DB
}

func (s testSQLiteStore) DB() *sql.DB { return s.db }

func (s testSQLiteStore) WithImmediateTx(fn func() error) error {
	if _, err := s.db.Exec(`BEGIN IMMEDIATE`); err != nil {
		return err
	}
	if err := fn(); err != nil {
		_, _ = s.db.Exec(`ROLLBACK`)
		return err
	}
	_, err := s.db.Exec(`COMMIT`)
	return err
}

func (s testSQLiteStore) Close() { _ = s.db.Close() }

func openTestLedgerDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteutil.DSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	const schema = `
CREATE TABLE IF NOT EXISTS read_ledger(conversation_id TEXT, generation_id INT, resolved_path TEXT, start_line INT, end_line INT, sha256 TEXT, turn_num INT, hit_count INT, updated_at INT, PRIMARY KEY(conversation_id, generation_id, resolved_path, start_line, end_line));
CREATE TABLE IF NOT EXISTS read_ledger_stats(conversation_id TEXT, turn_num INT, saved_count INT, updated_at INT, PRIMARY KEY(conversation_id, turn_num));
CREATE TABLE IF NOT EXISTS session_meta(conversation_id TEXT PRIMARY KEY, generation_id INT, last_transcript_bytes INT, turn_count INT, est_tokens INT);
`
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		t.Fatalf("init schema: %v", err)
	}
	return db
}

func TestParseCapsulePivots(t *testing.T) {
	t.Parallel()
	capsule := strings.Join([]string{
		"# slice: Alpha (budget: 2000 tokens)",
		"[Hop 0] pkg/alpha.go:3-15 (Alpha)",
		"[Hop 1] pkg/beta.go:10-20 (Beta)",
		"[Hop 1] pkg/big.go:1-120 snippet (BigWorker)",
		"[Hop 2] pkg/gamma.go:1-5 (Gamma)",
		"[Hop 0] invalid-no-colon (Bad)",
		"[Hop 0] pkg/bad.go:20-10 (Reversed)",
		"[Hop 0] pkg/zero.go:0-5 (ZeroStart)",
		"[Hop 0] pkg/nan.go:a-b (NotInt)",
	}, "\n")
	got := ParseCapsulePivots(capsule)
	want := []CapsulePivot{
		{Path: "pkg/alpha.go", StartLine: 3, EndLine: 15},
		{Path: "pkg/beta.go", StartLine: 10, EndLine: 20},
	}
	if len(got) != len(want) {
		t.Fatalf("ParseCapsulePivots = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ParseCapsulePivots[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestCanonicalizePathAndNormalizeLineRange(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	sub := filepath.Join(root, "pkg")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(sub, "shared.go")
	if err := os.WriteFile(file, []byte("package pkg\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(root, linkDir); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	c1 := CanonicalizePath(root, "pkg/shared.go")
	c2 := CanonicalizePath(linkDir, "pkg/shared.go")
	if c1 == "" || c1 != c2 {
		t.Fatalf("CanonicalizePath symlink mismatch: c1=%q c2=%q", c1, c2)
	}
	if got := CanonicalizePath("", "   "); got != "" {
		t.Errorf("CanonicalizePath(blank) = %q, want empty", got)
	}
	if got := CanonicalizePath("", "pkg/shared.go"); !filepath.IsAbs(filepath.FromSlash(got)) {
		t.Errorf("CanonicalizePath(\"\", rel) = %q, want absolute", got)
	}

	rangeCases := []struct {
		s, e, total, wantS, wantE int
	}{
		{0, 0, 50, 1, 50},
		{-10, 0, 50, 41, 50},
		{-100, 0, 20, 1, 20},
		{10, 999, 50, 10, 50},
		{30, 10, 50, 30, 30},
		{1, 1, 0, 1, 1},
	}
	for _, tc := range rangeCases {
		gotS, gotE := NormalizeLineRange(tc.s, tc.e, tc.total)
		if gotS != tc.wantS || gotE != tc.wantE {
			t.Errorf("NormalizeLineRange(%d,%d,%d) = %d..%d, want %d..%d", tc.s, tc.e, tc.total, gotS, gotE, tc.wantS, tc.wantE)
		}
	}
}

func TestShellTokenizersAndHeredocHelpers(t *testing.T) {
	t.Parallel()
	if !IsEnvAssignToken("FOO=bar") || !IsEnvAssignToken("_A1=2") {
		t.Error("expected valid env assignments to pass IsEnvAssignToken")
	}
	for _, bad := range []string{"", "=x", "1A=x", "FOO", "A-B=1"} {
		if IsEnvAssignToken(bad) {
			t.Errorf("IsEnvAssignToken(%q) = true, want false", bad)
		}
	}
	toks := TokenizeShellSegment(`FOO="a b" 'c d' $'e\'f' g\ h`)
	wantToks := []string{"FOO=a b", "c d", "e'f", "g h"}
	if len(toks) != len(wantToks) {
		t.Fatalf("TokenizeShellSegment = %q, want %q", toks, wantToks)
	}
	for i := range wantToks {
		if toks[i] != wantToks[i] {
			t.Errorf("TokenizeShellSegment[%d] = %q, want %q", i, toks[i], wantToks[i])
		}
	}
	delim, next := ParseHeredocDelim("<<-'EOF' rest", 2)
	if delim != "EOF" || next != 8 {
		t.Errorf("ParseHeredocDelim = (%q, %d), want (\"EOF\", 8)", delim, next)
	}
	body := "line1\n\tEOF\nafter"
	if pos := SkipHeredocBodies(body, 0, []string{"EOF"}); body[pos:] != "after" {
		t.Errorf("SkipHeredocBodies tail = %q, want \"after\"", body[pos:])
	}
	if end := SkipLineComment("# comment\nnext", 0); end != 8 {
		t.Errorf("SkipLineComment = %d, want 8", end)
	}
	if idx, q := AdvanceShellQuote(`"a\"b"`, 2, '"'); idx != 3 || q != '"' {
		t.Errorf("AdvanceShellQuote escaped = (%d, %q), want (3, '\"')", idx, q)
	}
	if idx, q := AdvanceShellQuote(`"a"`, 2, '"'); idx != 2 || q != 0 {
		t.Errorf("AdvanceShellQuote close = (%d, %q), want (2, 0)", idx, q)
	}
}

func TestSQLiteReadLedgerLifecycleAndPreWriteEdit(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "ledger.db")
	orig := strings.Join([]string{
		"package sample",
		"func Line2() int { return 2 }",
		"func Line3() int { return 3 }",
		"func Line4() int { return 4 }",
		"func Line5() int { return 5 }",
		"func Line6() int { return 6 }",
	}, "\n") + "\n"
	target := filepath.Join(root, "sample.go")
	if err := os.WriteFile(target, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	fixedNow := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	hooks := Hooks{
		OpenSessionStore: func(_, _ string) (DBStore, string, error) {
			return testSQLiteStore{db: openTestLedgerDB(t, dbPath)}, root, nil
		},
		Now: func() time.Time { return fixedNow },
	}

	if err := RegisterCapsulePivots(hooks, root, "", "conv-1", 1, []CapsulePivot{{Path: "sample.go", StartLine: 1, EndLine: 2}}); err != nil {
		t.Fatalf("RegisterCapsulePivots: %v", err)
	}
	if dec, err := CheckAndRecordReadInSession(hooks, root, "", root, "conv-1", "sample.go", 1, 2, 1); err != nil || dec.Allowed || !strings.HasPrefix(dec.Reason, "[UNCHANGED]") {
		t.Fatalf("pre-seeded read = %+v, err=%v; want deny [UNCHANGED]", dec, err)
	}
	if dec, err := CheckAndRecordReadInSession(hooks, root, "", root, "conv-1", "sample.go", 5, 6, 1); err != nil || !dec.Allowed {
		t.Fatalf("unread tail 5..6 = %+v, err=%v; want allow", dec, err)
	}

	// Edit line 3 (+3 lines): head 1..2 stays stamped, tail 5..6 shifts to 8..9.
	oldStr := "func Line3() int { return 3 }\n"
	newStr := "func Line3() int {\n\tx := 3\n\treturn x\n}\n"
	if dec, err := CheckPreWriteHashWithEdit(hooks, root, "", root, "conv-1", "sample.go", map[string]any{"Target": oldStr, "Replacement": newStr}); err != nil || !dec.Allowed {
		t.Fatalf("pre-write edit = %+v, err=%v; want allow", dec, err)
	}
	if err := os.WriteFile(target, []byte(strings.ReplaceAll(orig, oldStr, newStr)), 0o600); err != nil {
		t.Fatal(err)
	}
	if dec, err := CheckAndRecordReadInSession(hooks, root, "", root, "conv-1", "sample.go", 1, 2, 2); err != nil || dec.Allowed {
		t.Fatalf("head 1..2 after edit = %+v, err=%v; want deny [UNCHANGED]", dec, err)
	}
	if dec, err := CheckAndRecordReadInSession(hooks, root, "", root, "conv-1", "sample.go", 8, 9, 2); err != nil || dec.Allowed {
		t.Fatalf("shifted tail 8..9 after edit = %+v, err=%v; want deny [UNCHANGED]", dec, err)
	}
	// External modification triggers [MODIFIED] on pre-write check.
	if err := os.WriteFile(target, []byte(orig+"// external\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if dec, err := CheckPreWriteHashWithEdit(hooks, root, "", root, "conv-1", "sample.go", nil); err != nil || dec.Allowed || !strings.HasPrefix(dec.Reason, "[MODIFIED]") {
		t.Fatalf("external edit pre-write = %+v, err=%v; want deny [MODIFIED]", dec, err)
	}
}

func TestParseBashFileReadsV910All18Patterns(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cmd  string
		want []BashFileRead
	}{
		{"01 cat", "cat a.go", []BashFileRead{{Tool: "cat", Path: "a.go", StartLine: 1}}},
		{"02 cat -n", "cat -n a.go", []BashFileRead{{Tool: "cat", Path: "a.go", StartLine: 1}}},
		{"03 head -n", "head -n 50 a.go", []BashFileRead{{Tool: "head", Path: "a.go", StartLine: 1, EndLine: 50}}},
		{"04 head -N", "head -25 a.go", []BashFileRead{{Tool: "head", Path: "a.go", StartLine: 1, EndLine: 25}}},
		{"05 tail -n", "tail -n 30 a.go", []BashFileRead{{Tool: "tail", Path: "a.go", StartLine: -30, TailLines: 30}}},
		{"06 tail +N", "tail -n +15 a.go", []BashFileRead{{Tool: "tail", Path: "a.go", StartLine: 15}}},
		{"07 sed range", "sed -n '1,120p' a.go", []BashFileRead{{Tool: "sed", Path: "a.go", StartLine: 1, EndLine: 120}}},
		{"08 sed quit", "sed -n '10,50p;51q' a.go", []BashFileRead{{Tool: "sed", Path: "a.go", StartLine: 10, EndLine: 50}}},
		{"09 nl", "nl -ba a.go", []BashFileRead{{Tool: "nl", Path: "a.go", StartLine: 1}}},
		{"10 bat range", "bat --paging=never -r 10:80 a.go", []BashFileRead{{Tool: "bat", Path: "a.go", StartLine: 10, EndLine: 80}}},
		{"11a multi-range sed", "sed -n '1,10p;20,30p' a.go", []BashFileRead{
			{Tool: "sed", Path: "a.go", StartLine: 1, EndLine: 10},
			{Tool: "sed", Path: "a.go", StartLine: 20, EndLine: 30},
		}},
		{"11b multi -e sed", "sed -n -e '1,10p' -e '20,30p;31q' a.go", []BashFileRead{
			{Tool: "sed", Path: "a.go", StartLine: 1, EndLine: 10},
			{Tool: "sed", Path: "a.go", StartLine: 20, EndLine: 30},
		}},
		{"12a awk NR range", "awk 'NR>=1 && NR<=10' a.go", []BashFileRead{{Tool: "awk", Path: "a.go", StartLine: 1, EndLine: 10}}},
		{"12b awk NR range with print", `awk 'NR>=10 && NR<=25 {print NR": "$0}' a.go`, []BashFileRead{{Tool: "awk", Path: "a.go", StartLine: 10, EndLine: 25}}},
		{"12c awk comma range", "awk 'NR==5,NR==15' a.go", []BashFileRead{{Tool: "awk", Path: "a.go", StartLine: 5, EndLine: 15}}},
		{"12d awk full print", "awk '{print}' a.go", []BashFileRead{{Tool: "awk", Path: "a.go", StartLine: 1}}},
		{"13a python3 open", `python3 -c "print(open('a.go').read())"`, []BashFileRead{{Tool: "python", Path: "a.go", StartLine: 1}}},
		{"13b python3 open slice", `python3 -c "print(''.join(open('a.go').readlines()[10:30]))"`, []BashFileRead{{Tool: "python", Path: "a.go", StartLine: 11, EndLine: 30}}},
		{"13c python3 Path read_text", `python3 -c "from pathlib import Path; print(Path('sub/b.go').read_text())"`, []BashFileRead{{Tool: "python", Path: "sub/b.go", StartLine: 1}}},
		{"13d python3 open encoding", `python3 -c "print(open('a.go', encoding='ascii').read())"`, []BashFileRead{{Tool: "python", Path: "a.go", StartLine: 1}}},
		{"14a grep -n empty", "grep -n '' a.go", []BashFileRead{{Tool: "grep", Path: "a.go", StartLine: 1}}},
		{"14b grep -n caret", "grep -n '^' a.go", []BashFileRead{{Tool: "grep", Path: "a.go", StartLine: 1}}},
		{"14c rg -n empty", "rg -n '' a.go", []BashFileRead{{Tool: "rg", Path: "a.go", StartLine: 1}}},
		{"15a stderr redirect", "cat a.go 2>/dev/null", []BashFileRead{{Tool: "cat", Path: "a.go", StartLine: 1}}},
		{"15b stderr space and 2>&1", "head -n 20 a.go 2> /dev/null && sed -n '1,10p' b.go 2>&1", []BashFileRead{
			{Tool: "head", Path: "a.go", StartLine: 1, EndLine: 20},
			{Tool: "sed", Path: "b.go", StartLine: 1, EndLine: 10},
		}},
		{"16a trailing or true", "cat a.go || true", []BashFileRead{{Tool: "cat", Path: "a.go", StartLine: 1}}},
		{"16b combined stderr and or true", "grep -n '' a.go 2>/dev/null || true", []BashFileRead{{Tool: "grep", Path: "a.go", StartLine: 1}}},
		{"17a multi-file head", "head -n 20 a.go b.go", []BashFileRead{
			{Tool: "head", Path: "a.go", StartLine: 1, EndLine: 20},
			{Tool: "head", Path: "b.go", StartLine: 1, EndLine: 20},
		}},
		{"17b multi-file tail", "tail -n 15 a.go b.go", []BashFileRead{
			{Tool: "tail", Path: "a.go", StartLine: -15, TailLines: 15},
			{Tool: "tail", Path: "b.go", StartLine: -15, TailLines: 15},
		}},
		{"18a simple subshell", "(cd sub && cat a.go)", []BashFileRead{{Tool: "cat", Path: "sub/a.go", StartLine: 1}}},
		{"18b scoped subshell cd", "(cd sub && head -n 20 a.go) && cat b.go", []BashFileRead{
			{Tool: "head", Path: "sub/a.go", StartLine: 1, EndLine: 20},
			{Tool: "cat", Path: "b.go", StartLine: 1},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ParseBashFileReads(tc.cmd)
			if len(got) != len(tc.want) {
				t.Fatalf("ParseBashFileReads(%q) = %+v (len %d), want %+v (len %d)", tc.cmd, got, len(got), tc.want, len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("ParseBashFileReads(%q)[%d] = %+v, want %+v", tc.cmd, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestParseBashFileReadsV910MustFailSafety(t *testing.T) {
	t.Parallel()
	mustIgnore := []string{
		"sed -n '1,10p;1w out.txt' a.go",
		"sed -n '1,10p;1e id' a.go",
		`awk 'NR>=1 && NR<=10 {print > "out.txt"}' a.go`,
		`awk 'BEGIN{system("id")} NR>=1 && NR<=10' a.go`,
		"awk -f prog.awk a.go",
		"awk '/func/' a.go",
		`python3 -c "open('a.go', 'w').write('x')"`,
		`python3 -c "open('a.go', 'a').write('x')"`,
		`python3 -c "open('a.go', 'r+').read()"`,
		`python3 -c "from pathlib import Path; Path('a.go').write_text('x')"`,
		`python3 -c "import subprocess; open('a.go').read(); subprocess.run(['ls'])"`,
		`python3 -c "open('$FILE').read()"`,
		"grep -n 'func' a.go",
		"grep -nv '' a.go",
		"grep -c '' a.go",
		"grep -l '' a.go",
		"grep -F -n '^' a.go",
		"cat a.go 2> err.log",
		"cat a.go 1>/dev/null",
		"cat a.go > out.txt",
		"cat a.go || cat b.go",
		"cat a.go || echo fail",
		"cat a.go || true extra",
		"cd sub || true; cat a.go",
		"(cd sub && cat $(echo a.go))",
		"(for f in a.go; do cat a.go; done)",
		"(cd sub && cat a.go | head -n 5)",
		"(cd sub && cat a.go) | head -n 5",
		"((cd sub && cat a.go))",
		"(cd sub && cat a.go) > out.txt",
	}
	for _, cmd := range mustIgnore {
		if got := ParseBashFileReads(cmd); len(got) != 0 {
			t.Errorf("ParseBashFileReads(%q) = %+v, want empty (must-fail)", cmd, got)
		}
	}
}

func TestParseBashFileReadsNewlinesPrefixesAndReadOnlyPipes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cmd  string
		want []BashFileRead
	}{
		{"newline chain", "cat a.go\ncat b.go", []BashFileRead{{Tool: "cat", Path: "a.go", StartLine: 1}, {Tool: "cat", Path: "b.go", StartLine: 1}}},
		{"crlf chain", "cat a.go\r\ncat b.go", []BashFileRead{{Tool: "cat", Path: "a.go", StartLine: 1}, {Tool: "cat", Path: "b.go", StartLine: 1}}},
		{"export PATH with vars and nl piped to sed -n", "export PATH=\"$HOME/.local/bin:$PATH\"\nnl -ba internal/search/hook_pretool.go | sed -n '1,50p'", []BashFileRead{{Tool: "nl", Path: "internal/search/hook_pretool.go", StartLine: 1, EndLine: 50}}},
		{"export unset set cd prefixes with cat piped to head", "export FOO=\"$BAR\"\nunset DEBUG\nset -euo pipefail\ncd internal/search\ncat hook_pretool.go | head -n 40", []BashFileRead{{Tool: "cat", Path: "internal/search/hook_pretool.go", StartLine: 1, EndLine: 40}}},
		{"sed -n piped to nl preserves line range", "sed -n '1,80p' foo.go | nl -ba", []BashFileRead{{Tool: "sed", Path: "foo.go", StartLine: 1, EndLine: 80}}},
		{"head piped to tail narrows window", "head -n 100 foo.go | tail -n 20", []BashFileRead{{Tool: "head", Path: "foo.go", StartLine: 81, EndLine: 100}}},
		{"cat piped to tail +N then head", "cat foo.go | tail -n +15 | head -n 20", []BashFileRead{{Tool: "cat", Path: "foo.go", StartLine: 15, EndLine: 34}}},
		{"cat piped to tail -n N", "cat foo.go | tail -n 25", []BashFileRead{{Tool: "cat", Path: "foo.go", StartLine: -25, EndLine: 0, TailLines: 25}}},
		{"read-only filters grep awk cut sort uniq wc tr jq column expand fold rev", "nl -ba a.go | awk '{print $1}' | grep '[0-9]' | sort | uniq | wc -l\ncat b.go | cut -f1 | tr a-z A-Z | fold -w 80 | expand | rev | column -t\ncat c.json | jq '.items' | rg foo", []BashFileRead{{Tool: "nl", Path: "a.go", StartLine: 1}, {Tool: "cat", Path: "b.go", StartLine: 1}, {Tool: "cat", Path: "c.json", StartLine: 1}}},
		{"heredoc body ignored while following read is tracked", "cat <<'EOF'\ncat fake_inside_heredoc.go\nEOF\nhead -n 30 real.go", []BashFileRead{{Tool: "head", Path: "real.go", StartLine: 1, EndLine: 30}}},
		{"comments and backslash line continuation", "# inspect file\nexport PATH=\"$HOME/.local/bin:$PATH\"\ncat \\\n  foo.go | sed -n '10,40p'", []BashFileRead{{Tool: "cat", Path: "foo.go", StartLine: 10, EndLine: 40}}},
		{"quoted newline stays inside argument", "echo \"line1\ncat fake.go\"\ncat real.go", []BashFileRead{{Tool: "cat", Path: "real.go", StartLine: 1}}},
		{"multi-command read-only sed scripts pass", "nl -ba a.go | sed -n '10p;20,30p'\ncat b.go | sed -n '1,50{=;p}'", []BashFileRead{{Tool: "nl", Path: "a.go", StartLine: 1}, {Tool: "cat", Path: "b.go", StartLine: 1}}},
		{"absolute read still tracked after unparseable cd", "cd \"$REPO\"\ncat /abs/a.go\ncat rel.go\ncd sub 2>/dev/null\ncat /abs/b.go", []BashFileRead{{Tool: "cat", Path: "/abs/a.go", StartLine: 1}, {Tool: "cat", Path: "/abs/b.go", StartLine: 1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ParseBashFileReads(tc.cmd)
			if len(got) != len(tc.want) {
				t.Fatalf("ParseBashFileReads(%q) = %+v (len %d), want %+v (len %d)", tc.cmd, got, len(got), tc.want, len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("ParseBashFileReads(%q)[%d] = %+v, want %+v", tc.cmd, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestParseBashFileReadsMustFailMutatingPipesRedirectsAndSubstitutions(t *testing.T) {
	t.Parallel()
	mustIgnore := []string{
		"cat foo.go | tee out.txt",
		"nl -ba foo.go | sed -i 's/a/b/'",
		"nl -ba foo.go | sed 's/a/b/'",
		"cat foo.go | sh",
		"cat foo.go | bash",
		"cat foo.go | python3 -c 'print(1)'",
		"cat foo.go | python -",
		"cat foo.go | go run .",
		"cat foo.go | xargs rm",
		"cat foo.go | rm -f bar",
		"cat foo.go | mv a b",
		"cat foo.go | cp a b",
		"cat foo.go | sort -o sorted.go",
		"cat foo.go | uniq in.txt out.txt",
		"cat foo.go | tail -f",
		`cat foo.go | awk '{print > "out.txt"}'`,
		`cat foo.go | awk '{system("id")}'`,
		`cat foo.go | awk '{system ("id")}'`,
		`cat foo.go | awk '{print | "sh"}'`,
		`cat foo.go | awk '{print |& "sh"}'`,
		`cat foo.go | awk -f script.awk`,
		"nl -ba foo.go | sed -n '1,10wout.txt'",
		"nl -ba foo.go | sed -n '1,10Wout.txt'",
		"nl -ba foo.go | sed -n '1,5w /tmp/x'",
		"nl -ba foo.go | sed -n 's/a/b/wout.txt'",
		"cat foo.go | sed -n '1e'",
		"cat foo.go | sed -n '1r /etc/passwd'",
		"cat foo.go | sed -n '1R /etc/passwd'",
		"cat foo.go | sed -n '/a[/p;#]/e'",
		"cat foo.go | sed -ne1,10wout.txt",
		"cat foo.go | sed -n -e '1p' --expression=1e",
		"cd \"$REPO\"\ncat a.go",
		"cd $DIR\ncat a.go",
		"cd sub 2>/dev/null\ncat a.go",
		"cd dir*\ncat a.go",
		"builtin cd \"$DIR\"\ncat a.go",
		"pushd \"$DIR\"\ncat a.go",
		"popd 2>/dev/null\ncat a.go",
		"FOO=1 cd \"$DIR\"\ncat a.go",
		"CDPATH=/tmp cd sub\ncat a.go",
		"cd sub | cat\ncat a.go",
		"cd sub > out.txt\ncat a.go",
		"2>/dev/null cd sub\ncat a.go",
		"cat foo.go > out.txt",
		"cat foo.go >> out.txt",
		"cat foo.go | head -n 10 > out.txt",
		"cat foo.go | head -n 10 other.go",
		"cat foo.go > b.go\ncat b.go",
		"cat <<'EOF' > b.go\nhi\nEOF\ncat b.go",
		"export X=$(date)\ncat foo.go",
		"cat $(echo foo.go)",
		"cat \"$(echo foo.go)\"",
		"cat `echo foo.go`",
		"cat <(echo hi)",
		"cat $TARGET_FILE",
		`cat "$TARGET_FILE"`,
		"cat *.go | head -n 10",
		"grep ParseBashFileReads foo.go | head -n 10",
		"cat <<'EOF'\ncat fake.go\nEOF",
	}
	for _, cmd := range mustIgnore {
		if got := ParseBashFileReads(cmd); len(got) != 0 {
			t.Errorf("ParseBashFileReads(%q) = %+v, want empty (must-fail safety)", cmd, got)
		}
	}
}

func TestParseBashPythonAndAwkVerbatimOnly(t *testing.T) {
	t.Parallel()
	mustIgnore := []string{
		`python3 -c 'print(len(open("a.go").readlines()))'`,
		`python3 -c 'import json; print(json.load(open("cfg.json"))["k"])'`,
		`python3 -c 'import json; print(json.loads(open("cfg.json").read())["k"])'`,
		`python3 -c 'import yaml; print(yaml.safe_load(open("cfg.yaml")))'`,
		`python3 -c 'import re; print(re.findall("func", open("a.go").read()))'`,
		`python3 -c 'print(sum(1 for _ in open("a.go")))'`,
		`python3 -c 'print(open("a.go").read().count("foo"))'`,
		`python3 -c 'open("a.go").read()'`,
		`awk '{print $1}' a.go`,
		`awk 'NR>=1&&NR<=50 {print $1}' a.go`,
		`awk '{print NR}' a.go`,
		`awk '{print $1, $2}' a.go`,
	}
	for _, cmd := range mustIgnore {
		if got := ParseBashFileReads(cmd); len(got) != 0 {
			t.Errorf("ParseBashFileReads(%q) = %+v, want empty", cmd, got)
		}
	}
	mustCatch := []struct {
		cmd  string
		want BashFileRead
	}{
		{`python3 -c 'print(open("a.go").read())'`, BashFileRead{Tool: "python", Path: "a.go", StartLine: 1}},
		{`python3 -c 'from pathlib import Path; print(Path("a.go").read_text())'`, BashFileRead{Tool: "python", Path: "a.go", StartLine: 1}},
		{`python3 -c 'print(open("a.go").readlines()[0:50])'`, BashFileRead{Tool: "python", Path: "a.go", StartLine: 1, EndLine: 50}},
		{`python3 -c 'import sys; sys.stdout.write(open("a.go").read())'`, BashFileRead{Tool: "python", Path: "a.go", StartLine: 1}},
		{`awk '1' a.go`, BashFileRead{Tool: "awk", Path: "a.go", StartLine: 1}},
		{`awk 'NR>=1&&NR<=50' a.go`, BashFileRead{Tool: "awk", Path: "a.go", StartLine: 1, EndLine: 50}},
		{`awk '{print}' a.go`, BashFileRead{Tool: "awk", Path: "a.go", StartLine: 1}},
		{`awk '{print $0}' a.go`, BashFileRead{Tool: "awk", Path: "a.go", StartLine: 1}},
		{`awk '{print NR, $0}' a.go`, BashFileRead{Tool: "awk", Path: "a.go", StartLine: 1}},
		{`awk '{print NR ":" $0}' a.go`, BashFileRead{Tool: "awk", Path: "a.go", StartLine: 1}},
	}
	for _, tc := range mustCatch {
		got := ParseBashFileReads(tc.cmd)
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("ParseBashFileReads(%q) = %+v, want [%+v]", tc.cmd, got, tc.want)
		}
	}
}

func TestParseBashFileReadsChainsCdAndSplit(t *testing.T) {
	t.Parallel()
	cases := []struct {
		cmd  string
		want []BashFileRead
	}{
		{"cat a.go && head -n 10 b.go", []BashFileRead{{Tool: "cat", Path: "a.go", StartLine: 1}, {Tool: "head", Path: "b.go", StartLine: 1, EndLine: 10}}},
		{"nl -ba a.go ; bat -r 5:25 b.go", []BashFileRead{{Tool: "nl", Path: "a.go", StartLine: 1}, {Tool: "bat", Path: "b.go", StartLine: 5, EndLine: 25}}},
		{"cd dir && sed -n '1,10p' foo.go", []BashFileRead{{Tool: "sed", Path: "dir/foo.go", StartLine: 1, EndLine: 10}}},
		{"head -n 20 a.go && cat b.go", []BashFileRead{{Tool: "head", Path: "a.go", StartLine: 1, EndLine: 20}, {Tool: "cat", Path: "b.go", StartLine: 1}}},
		{"cd a/b && cd .. && cat c.go", []BashFileRead{{Tool: "cat", Path: "a/c.go", StartLine: 1}}},
		{`sed -n '10,20p;21q' "my dir/a;b.go" && cat 'sub/file name.go'`, []BashFileRead{{Tool: "sed", Path: "my dir/a;b.go", StartLine: 10, EndLine: 20}, {Tool: "cat", Path: "sub/file name.go", StartLine: 1}}},
		{"make test && cat a.go", []BashFileRead{{Tool: "cat", Path: "a.go", StartLine: 1}}},
		{"cd ~ && cat a.go; cd - && cat b.go; cd -P sub && cat c.go", nil},
		{"cd sub && cat /abs/path.go", []BashFileRead{{Tool: "cat", Path: "/abs/path.go", StartLine: 1}}},
		{"(cd sub && cat a.go)", []BashFileRead{{Tool: "cat", Path: "sub/a.go", StartLine: 1}}},
	}
	for _, tc := range cases {
		got := ParseBashFileReads(tc.cmd)
		if len(got) != len(tc.want) {
			t.Errorf("ParseBashFileReads(%q) len=%d (%+v), want %d (%+v)", tc.cmd, len(got), got, len(tc.want), tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("ParseBashFileReads(%q)[%d] = %+v, want %+v", tc.cmd, i, got[i], tc.want[i])
			}
		}
	}
	for _, cmd := range []string{
		"grep foo bar | head", "cat a.go > out.txt", "cat a.go >> out.txt", "cat < a.go",
		"cat $(echo a.go)", "cat \"$(echo a.go)\"", "cat `echo a.go`", "cat *.go",
		"cat a?.go", "cat [ab].go", "cat a.go &", "cat a.go &&& cat b.go",
		"(cd sub && cat $(echo a.go))", "(for f in a.go; do cat a.go; done)",
		"(cd sub && cat a.go | head -n 5)", "(cd sub && cat a.go) | head -n 5",
		"cat a.go || cat b.go", "cat 'unterminated.go",
	} {
		if got := ParseBashFileReads(cmd); len(got) != 0 {
			t.Errorf("ParseBashFileReads(%q) = %+v, want empty", cmd, got)
		}
	}
	if stmts, ok := splitBashReadStatements(`cd sub && sed -n '1,10p;11q' "a;b.go" ; cat c\;d.go`, false); !ok || len(stmts) != 3 {
		t.Fatalf("splitBashReadStatements = %+v, %v; want 3 statements", stmts, ok)
	}
	for _, bad := range []string{"", "  ; && ", "cat a.go &", "cat a.go &&& b", "cat a.go || b", "cat a.go |& head", "cat $(echo a.go)", "cat 'open", "(cd sub && cat a.go)"} {
		if stmts, ok := splitBashReadStatements(bad, false); ok || len(stmts) != 0 {
			t.Errorf("splitBashReadStatements(%q, false) = %+v, %v; want nil, false", bad, stmts, ok)
		}
	}
}
