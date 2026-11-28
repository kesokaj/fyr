package ingest

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/sqliteutil"
)

func TestOptionsValidateMustPassAndMustFail(t *testing.T) {
	t.Parallel()
	passCases := []Options{
		{Home: "/h"},
		{Home: "/h", Source: "/sb", Label: "eval=row1"},
		{Home: "/h", Source: "/sb", Label: "eval=row1", Outcome: "pass", Run: "r1"},
		{Home: "/h", Source: "/sb", Label: "eval=row1", Outcome: "fail"},
		{Home: "/h", Source: "/sb", Label: "eval=row1", Outcome: "error"},
	}
	for _, tc := range passCases {
		if err := tc.Validate(); err != nil {
			t.Errorf("Validate(%+v) unexpected error: %v", tc, err)
		}
	}
	failCases := []Options{
		{Source: "/sb"},
		{Source: "/sb", Label: "badlabel"},
		{Source: "/sb", Label: "=val"},
		{Source: "/sb", Label: "k="},
		{Source: "/sb", Label: "k=v", Outcome: "bogus"},
		{Label: "k=v"},
		{Outcome: "pass"},
		{Run: "r1"},
		{Source: "/sb", Label: "k=v", Run: "bad\nrun"},
		{Source: "/sb", Label: "k=v", Run: strings.Repeat("x", 121)},
	}
	for _, tc := range failCases {
		if err := tc.Validate(); err == nil {
			t.Errorf("Validate(%+v) = nil, want error", tc)
		}
	}
}

func TestVerbHeredocAndRecordReadMustPassAndMustFail(t *testing.T) {
	t.Parallel()
	if got := Verb("git -C repo add -A && go run ./cmd/fyr metrics show --json"); !reflect.DeepEqual(got, []string{"git add -A", "fyr metrics show --json"}) {
		t.Errorf("Verb(go run fyr) = %v", got)
	}
	if got := Verb("go run ./other/tool -v file.go"); !reflect.DeepEqual(got, []string{"go run -v"}) {
		t.Errorf("Verb(go run unknown) = %v", got)
	}
	if got := Verb("gh secret123 --bad_Flag"); !reflect.DeepEqual(got, []string{"gh"}) {
		t.Errorf("Verb(unlisted subcommand/flag) = %v", got)
	}
	if got := Verb("cat <<'EOF'\nrm -rf /nope\nEOF\necho ok"); !reflect.DeepEqual(got, []string{"cat", "echo"}) {
		t.Errorf("Verb(heredoc) = %v", got)
	}
	if got := Verb("cat <<\nrm -rf /unterminated"); !reflect.DeepEqual(got, []string{"cat"}) {
		t.Errorf("Verb(unparsed heredoc) = %v", got)
	}
	if got := Verb("case $x in a|b) git status;; esac; (ls -la 2>&1) # ignored"); !reflect.DeepEqual(got, []string{"git status", "ls -la"}) {
		t.Errorf("Verb(case+subshell+comment) = %v", got)
	}
	if got := RecordRead("fyr --home /tmp/h record get ADR-001"); got != "adr" {
		t.Errorf("RecordRead(get ADR) = %q, want adr", got)
	}
	if got := RecordRead("go run ./cmd/fyr record list"); got != "list" {
		t.Errorf("RecordRead(list) = %q, want list", got)
	}
	if got := RecordRead("fyr record get UNKNOWN-1"); got != "record" {
		t.Errorf("RecordRead(unknown id) = %q, want record", got)
	}
	if got := RecordRead("fyr search foo"); got != "" {
		t.Errorf("RecordRead(non-record) = %q, want empty", got)
	}
}

func TestWaveStampAndSessionModelNormalization(t *testing.T) {
	t.Parallel()
	enc := EncodeWaveStamp("eval=row1", "2026-11-28-01")
	lbl, raw := DecodeWaveStamp(enc)
	if lbl != "eval=row1" || raw != "2026-11-28-01" {
		t.Errorf("DecodeWaveStamp(%q) = (%q, %q)", enc, lbl, raw)
	}
	if l2, r2 := DecodeWaveStamp("2026-11-28-01"); l2 != "" || r2 != "2026-11-28-01" {
		t.Errorf("DecodeWaveStamp(plain) = (%q, %q)", l2, r2)
	}
	if got := NormalizeSessionModel("gemini-4-neon", map[string]bool{"gemini-4-neon": true, "gemini-4-neon-high": true}); got != "gemini-4-neon-high" {
		t.Errorf("NormalizeSessionModel = %q", got)
	}
	if got := HookDenialKind("[CLAMPED] too big"); got != "clamped" {
		t.Errorf("HookDenialKind(clamped) = %q", got)
	}
	if got := HookDenialKind("normal error"); got != "" {
		t.Errorf("HookDenialKind(normal) = %q", got)
	}
}

func TestHeredocDelimsArms(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		line     string
		unparsed bool
	}{
		{"cat <<< word", false},
		{"cat <<'EOF", true},
		{"cat <<''", true},
		{"cat << ;", true},
	} {
		ds, unparsed := heredocDelims(c.line)
		if ds != nil || unparsed != c.unparsed {
			t.Errorf("heredocDelims(%q) = %v, %v; want none, %v", c.line, ds, unparsed, c.unparsed)
		}
	}
}

func TestIsAssignmentIdentBounds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{"A=1", true},
		{"_a9=val", true},
		{"FOO_BAR=baz", true},
		{"z0=", true},
		{"9A=1", false},
		{"=1", false},
		{"NOEQ", false},
		{"a-b=1", false},
		{"a.b=1", false},
	}
	for _, tc := range cases {
		if got := isAssignment(tc.in); got != tc.want {
			t.Errorf("isAssignment(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestSD125ReadVetHistoryRowsOrderAndLedger(t *testing.T) {
	t.Parallel()
	sdb := filepath.Join(t.TempDir(), "search.db")
	db, err := sql.Open("sqlite", sqliteutil.DSN(sdb))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`CREATE TABLE vet_history(conversation_id TEXT, turn_num INT, intent TEXT, gate TEXT, score INT, raw_prompt TEXT, suggested_skills TEXT, pivot_files TEXT, created_at INT)`,
		`CREATE TABLE read_ledger_stats(conversation_id TEXT, turn_num INT, saved_count INT)`,
		"INSERT INTO vet_history VALUES ('C1\x1b', 2, 'FIX', 'PROCEED', 80, 'p2', '', '', 200)",
		"INSERT INTO vet_history VALUES ('C1\x1b', 1, 'FIX', 'PROCEED', 70, 'p1', '', '', 100)",
		"INSERT INTO read_ledger_stats VALUES (' C1\x1b ', 99, 5)",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	ledger, err := readLedgerSavings(db)
	if err != nil {
		t.Fatalf("readLedgerSavings: %v", err)
	}
	vets, err := (Deps{}).readVetHistoryRows(db, "/proj", "repo:proj", ledger)
	if err != nil || len(vets) != 2 {
		t.Fatalf("readVetHistoryRows len=%d err=%v", len(vets), err)
	}
	if vets[0].turnNum != 1 || vets[1].turnNum != 2 || vets[1].ledger != 5 || vets[0].convID != "C1?" {
		t.Fatalf("vets order/ledger = %+v", vets)
	}
}
