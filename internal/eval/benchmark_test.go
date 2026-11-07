package eval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

const benchHeader = "| id | setup | prompt | prompt2 | check | agent | grants | version | kind | cluster | expect | timeout | turns |\n|---|---|---|---|---|---|---|---|---|---|---|---|---|\n"

// TestSuiteGrammarBenchmark pins the 13-column benchmark suite header and
// its timeout (1m to 4h, whole minutes) and turns (1 to 12) cells
// (ADR-509, Item 24 of PLAN-2026-11-02-1806-fyr-speed).
func TestSuiteGrammarBenchmark(t *testing.T) {
	body := "Prose.\n\n" + benchHeader +
		"| `dflt` |  | p |  | true |  | named |  |  |  |  |  |  |\n" +
		"| `one` |  | p |  | true |  | named | 1 | benchmark |  |  | 45m | 1 |\n" +
		"| `pipe` |  | plan it | go all approved | true |  | wide: builds app | 2 | benchmark | booking |  | 4h | 12 |\n" +
		"| `hm` |  | p | next | true |  | named | 1 | capability |  |  | 1h30m | 3 |\n"
	rows, err := ParseSuite(body)
	if err != nil {
		t.Fatal(err)
	}
	want := []Row{
		defaulted(Row{ID: "dflt", Prompt: "p", Check: "true", Grants: "named"}),
		{ID: "one", Prompt: "p", Check: "true", Grants: "named", Version: 1, Kind: KindBenchmark, Cluster: "one", Timeout: 45 * time.Minute, Turns: 1},
		{ID: "pipe", Prompt: "plan it", Prompt2: "go all approved", Check: "true", Grants: "wide: builds app", Version: 2, Kind: KindBenchmark, Cluster: "booking", Timeout: 4 * time.Hour, Turns: 12},
		{ID: "hm", Prompt: "p", Prompt2: "next", Check: "true", Grants: "named", Version: 1, Kind: KindCapability, Cluster: "hm", Timeout: 90 * time.Minute, Turns: 3},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(rows), len(want), rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d:\n got %+v\nwant %+v", i, rows[i], want[i])
		}
	}

	row := func(p2, tail string) string {
		return benchHeader + "| `a` |  | p | " + p2 + " | true |  | named | 1 | benchmark |  |  | " + tail + " |\n"
	}
	for name, c := range map[string]struct{ body, want string }{
		"11 cells under 13-col header": {benchHeader + "| `a` |  | p |  | true |  | named |  |  |  |  |\n", "11 columns, want 13"},
		"timeout 0m":                   {row("", "0m | "), "timeout \"0m\": want whole minutes from 1m to 4h"},
		"timeout signed":               {row("", "+10m | "), "timeout \"+10m\": want whole minutes from 1m to 4h"},
		"timeout leading zero":         {row("", "010m | "), "timeout \"010m\": want whole minutes from 1m to 4h"},
		"timeout seconds":              {row("", "30s | "), "timeout \"30s\": want whole minutes from 1m to 4h"},
		"timeout over 4h (241m)":       {row("", "241m | "), "timeout \"241m\": want whole minutes from 1m to 4h"},
		"timeout over 4h (4h1m)":       {row("", "4h1m | "), "timeout \"4h1m\": want whole minutes from 1m to 4h"},
		"timeout word":                 {row("", "long | "), "timeout \"long\": want whole minutes from 1m to 4h"},
		"turns 0":                      {row("", " | 0"), "turns \"0\": want an integer from 1 to 12"},
		"turns leading zero":           {row("next", " | 02"), "turns \"02\": want an integer from 1 to 12"},
		"turns signed":                 {row("next", " | +2"), "turns \"+2\": want an integer from 1 to 12"},
		"turns 13":                     {row("next", " | 13"), "turns \"13\": want an integer from 1 to 12"},
		"turns word":                   {row("next", " | two"), "turns \"two\": want an integer from 1 to 12"},
		"turns 1 with prompt2":         {row("next", " | 1"), "turns 1 cannot have prompt2"},
		"turns 2 without prompt2":      {row("", " | 2"), "turns 2 requires prompt2"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseSuite(c.body); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("want error holding %q, got %v", c.want, err)
			}
		})
	}
}

// TestRunRowTimeout pins per-row timeout (ADR-509): a whole-minute Timeout
// sets --print-timeout <minutes>m and kill = Timeout + 5m; a sub-minute
// test Timeout kills a hanging agy after Timeout; and a Timeout outside
// 0..MaxTimeout is refused before any sandbox is created.
func TestRunRowTimeout(t *testing.T) {
	r := newRig(t)
	row := Row{ID: "t60", Prompt: "p", Check: "true", Grants: "named", Timeout: 90 * time.Minute}
	x := one(t, r.run(row))
	wantVerdict(t, x, StatusPass)
	args := strings.Split(strings.TrimSuffix(r.read("agy.args.1"), "\n"), "\n")
	if len(args) < 6 || args[4] != "--print-timeout" || args[5] != "90m" {
		t.Errorf("agy args = %q, want --print-timeout 90m", args)
	}

	rSlow := newRig(t)
	rSlow.on("hang", "")
	xSlow := one(t, rSlow.run(Row{ID: "short", Prompt: "p", Check: "false", Grants: "named", Timeout: 400 * time.Millisecond}))
	if xSlow.Status != StatusFail || xSlow.Cause != "timeout" || xSlow.AgyExit != -1 {
		t.Errorf("short timeout: status %s cause %s exit %d, want FAIL timeout -1", xSlow.Status, xSlow.Cause, xSlow.AgyExit)
	}
	if !strings.Contains(rSlow.out.String(), "agy timed out after 400ms") {
		t.Errorf("missing 400ms timeout note:\n%s", rSlow.out.String())
	}

	rRefuse := newRig(t)
	for _, bad := range []time.Duration{-time.Minute, MaxTimeout + time.Minute} {
		res, err := Run(context.Background(), rRefuse.opts(), []Row{{ID: "bad", Prompt: "p", Check: "true", Grants: "named", Timeout: bad}})
		if err == nil || !strings.Contains(err.Error(), "timeout") || res != nil {
			t.Errorf("timeout %s: got %v, %v; want refusal", bad, res, err)
		}
	}
	if _, err := os.Stat(filepath.Join(rRefuse.home, harness.StateDir, "eval")); !os.IsNotExist(err) {
		t.Errorf("refused timeout created sandbox root: %v", err)
	}
}

// TestRunScriptedTurns pins multi-turn execution up to MaxTurns (12),
// OUT3..OUT<N> in checkEnv, early stop when a session prints the handoff's
// "Nothing queued; start the next task in a fresh conversation." line or
// exits non-zero, and preflight refusals for invalid Turns.
func TestRunScriptedTurns(t *testing.T) {
	t.Run("four turns run in order", func(t *testing.T) {
		r := newRig(t)
		row := Row{
			ID:      "four",
			Prompt:  "start {{CLONE}}",
			Prompt2: "continue in {{SB}}",
			Turns:   4,
			Check:   `grep -q "session 1" "$OUT" && grep -q "session 2" "$OUT2" && grep -q "session 3" "$OUT3" && grep -q "session 4" "$OUT4"`,
			Grants:  "named",
		}
		x := one(t, r.run(row))
		wantVerdict(t, x, StatusPass)
		if got := strings.TrimSpace(r.read("agy.n")); got != "4" {
			t.Errorf("agy ran %s times, want 4", got)
		}
		if b, _ := os.ReadFile(filepath.Join(x.Dir, "err.txt")); string(b) != "stderr 1\nstderr 2\nstderr 3\nstderr 4\n" {
			t.Errorf("err.txt = %q, want 4 sessions' stderr", b)
		}
	})

	t.Run("stops early when nothing queued", func(t *testing.T) {
		r := newRig(t)
		r.on("done.2", "")
		row := Row{
			ID:      "early",
			Prompt:  "start",
			Prompt2: "continue",
			Turns:   5,
			Check:   `grep -q "Nothing queued" "$OUT2" && ! test -e "$OUT3"`,
			Grants:  "named",
		}
		x := one(t, r.run(row))
		wantVerdict(t, x, StatusPass)
		if got := strings.TrimSpace(r.read("agy.n")); got != "2" {
			t.Errorf("agy ran %s times after Nothing queued, want 2", got)
		}
	})

	t.Run("stops early on non-zero exit", func(t *testing.T) {
		r := newRig(t)
		r.on("exit.2", "1")
		row := Row{
			ID:      "fail2",
			Prompt:  "start",
			Prompt2: "continue",
			Turns:   4,
			Check:   `test -s "$OUT2" && ! test -e "$OUT3"`,
			Grants:  "named",
		}
		x := one(t, r.run(row))
		wantVerdict(t, x, StatusPass)
		if got := strings.TrimSpace(r.read("agy.n")); got != "2" {
			t.Errorf("agy ran %s times after exit 1 on turn 2, want 2", got)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		r := newRig(t)
		for _, bad := range []Row{
			{ID: "neg", Prompt: "p", Prompt2: "q", Check: "true", Grants: "named", Turns: -1},
			{ID: "over", Prompt: "p", Prompt2: "q", Check: "true", Grants: "named", Turns: MaxTurns + 1},
			{ID: "onep2", Prompt: "p", Prompt2: "q", Check: "true", Grants: "named", Turns: 1},
			{ID: "twonop2", Prompt: "p", Check: "true", Grants: "named", Turns: 2},
		} {
			if res, err := Run(context.Background(), r.opts(), []Row{bad}); err == nil || !strings.Contains(err.Error(), "turns") || res != nil {
				t.Errorf("row %s: got %v, %v; want turns refusal", bad.ID, res, err)
			}
		}
	})
}

// TestRunStageSecondsAndHiddenCheck pins SetupSeconds, SessionSeconds and
// CheckSeconds on eval.Result and verifies that a row's Check script stays
// out of the sandbox while agy runs and executes only at scoring (ADR-509).
func TestRunStageSecondsAndHiddenCheck(t *testing.T) {
	r := newRig(t)
	const secret = "HIDDEN_CHECK_SECRET_MARKER_509"
	r.on("check-absent", secret)
	row := Row{
		ID:     "stages",
		Setup:  "echo setup-ok > setup-ran.txt",
		Prompt: "build",
		Check:  `echo "` + secret + `" && test -f setup-ran.txt && grep -q "agy says ok" "$OUT"`,
		Grants: "named",
	}
	x := one(t, r.run(row))
	wantVerdict(t, x, StatusPass)
	if x.SetupSeconds < 0 || x.SessionSeconds < 0 || x.CheckSeconds < 0 {
		t.Errorf("stage seconds = setup %d, session %d, check %d; want all >= 0", x.SetupSeconds, x.SessionSeconds, x.CheckSeconds)
	}
	checkLog, err := os.ReadFile(filepath.Join(x.Dir, "check.log"))
	if err != nil || !strings.Contains(string(checkLog), secret) {
		t.Errorf("check.log after scoring = %q, %v; want %q", checkLog, err, secret)
	}
	for _, f := range []string{"agy.args.1", "agy.env.1", "settings.1"} {
		if strings.Contains(r.read(f), secret) {
			t.Errorf("%s leaked the check script marker", f)
		}
	}

	// Must-fail control: if Setup plants the check marker inside the sandbox
	// before agy runs, fakeAgy's mid-session check-absent guard exits 99.
	rLeak := newRig(t)
	rLeak.on("check-absent", secret)
	xLeak := one(t, rLeak.run(Row{
		ID:     "leaked",
		Setup:  `echo "` + secret + `" > leaked.txt`,
		Prompt: "build",
		Check:  `grep -q "agy says ok" "$OUT"`,
		Grants: "named",
	}))
	if xLeak.AgyExit != 99 {
		t.Errorf("leaked setup AgyExit = %d, want 99", xLeak.AgyExit)
	}
	wantVerdict(t, xLeak, StatusFail, ReasonCheck)

	rErr := newRig(t)
	xErr := one(t, rErr.run(Row{ID: "err", Setup: "exit 1", Prompt: "p", Check: "true", Grants: "named"}))
	wantVerdict(t, xErr, StatusError, ReasonSetup)
	if xErr.SetupSeconds != -1 || xErr.SessionSeconds != -1 || xErr.CheckSeconds != -1 {
		t.Errorf("failed setup stage seconds = %d, %d, %d; want -1, -1, -1", xErr.SetupSeconds, xErr.SessionSeconds, xErr.CheckSeconds)
	}
}
