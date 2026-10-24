package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/kesokaj/styrman-harness/internal/harness"
)

// Every usage error exits 2 with its reason on the verb's writer, as README
// § Daily commands, specs/sh-agent/delivery.md and dream.md state. These
// sites returned a plain error instead — exit 1, the message on stderr —
// until the project review of 2026-10-09 (row 4, BUG-168).
func TestUsageErrorsExit2(t *testing.T) {
	home := t.TempDir()
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"search without words":             {[]string{"search", "--home", home}, "give words"},
		"search --limit -1":                {[]string{"search", "--limit", "-1", "--home", home, "x"}, "--limit must be"},
		"adopt without a name":             {[]string{"adopt", "--repo", "../..", "--home", home}, "usage: sh-agent adopt"},
		"update --from-zip without a file": {[]string{"update", "--from-zip", "--dry-run", "--home", home}, "--from-zip needs a file"},
		"install --from-zip":               {[]string{"install", "--from-zip", "x.zip", "--home", home}, "--from-zip belongs to update"},
		// --vendor left with the second vendor (plan fyr Phase 2): a once-valid
		// value is refused like any undefined flag, before the verb runs, on
		// every verb that took it (BUG-171's rule).
		"install --vendor agy":             {[]string{"install", "--dry-run", "--repo", "../..", "--vendor", "agy", "--home", home}, "flag provided but not defined: -vendor"},
		"status --vendor agy":              {[]string{"status", "--repo", "../..", "--vendor", "agy", "--home", home}, "flag provided but not defined: -vendor"},
		"uninstall --dry-run --vendor agy": {[]string{"uninstall", "--dry-run", "--vendor", "agy", "--home", home}, "flag provided but not defined: -vendor"},
		"dream --vendor agy":               {[]string{"dream", "--all", "--dry-run", "--vendor", "agy", "--home", home}, "flag provided but not defined: -vendor"},
		"memory show --vendor agy":         {[]string{"memory", "show", "--vendor", "agy", "--home", home}, "flag provided but not defined: -vendor"},
		"metrics ingest --vendor agy":      {[]string{"metrics", "ingest", "--vendor", "agy", "--home", home}, "flag provided but not defined: -vendor"},
		"metrics show --context-target 0":  {[]string{"metrics", "show", "--context-target", "0", "--home", home}, "--context-target takes a token count above 0"},
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			err := run(tc.args, &out)
			var code exitCode
			if !errors.As(err, &code) || code != 2 {
				t.Fatalf("%v: want exit 2, got %v\n%s", tc.args, err, out.String())
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Errorf("%v: the reason %q is not on the writer:\n%s", tc.args, tc.want, out.String())
			}
		})
	}
}

// The version verb was the one verb no test drove through run() (project
// review 2026-10-09, row 13).
func TestVersionVerb(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"version"}, &out); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "sh-agent "+buildVersion()+"\n"; got != want {
		t.Errorf("version printed %q, want %q", got, want)
	}
}

// TestHelperMain is not a test: TestMainExitStatus runs this binary as a
// subprocess with SH_AGENT_HELPER_ARGS set and reads main's exit status.
func TestHelperMain(t *testing.T) {
	args := os.Getenv("SH_AGENT_HELPER_ARGS")
	if args == "" {
		t.Skip("helper process")
	}
	os.Args = append([]string{"sh-agent"}, strings.Fields(args)...)
	main()
}

// main exits 0 on success, with the verb's own status when run returns one
// (2 for a usage error), and 1 with "sh-agent: <error>" on stderr otherwise.
// Nothing read main's exit status until the project review of 2026-10-09
// (row 13).
func TestMainExitStatus(t *testing.T) {
	for name, tc := range map[string]struct {
		args   string
		code   int
		stdout string
		stderr string
	}{
		"version":         {"version", 0, "sh-agent ", ""},
		"usage error":     {"list --no-such-flag", 2, "", ""},
		"unknown command": {"no-such-verb", 1, "", "sh-agent: unknown command"},
		// The report path left with ADR-062: the word is no verb at all.
		"report is gone": {"report --dry-run", 1, "", `sh-agent: unknown command "report"`},
	} {
		t.Run(name, func(t *testing.T) {
			c := exec.Command(os.Args[0], "-test.run=^TestHelperMain$")
			c.Env = append(os.Environ(), "SH_AGENT_HELPER_ARGS="+tc.args)
			var stdout, stderr bytes.Buffer
			c.Stdout, c.Stderr = &stdout, &stderr
			err := c.Run()
			code := 0
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				code = ee.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if code != tc.code {
				t.Errorf("sh-agent %s: exit %d, want %d\nstdout: %s\nstderr: %s", tc.args, code, tc.code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), tc.stdout) {
				t.Errorf("sh-agent %s: stdout %q lacks %q", tc.args, stdout.String(), tc.stdout)
			}
			if !strings.Contains(stderr.String(), tc.stderr) {
				t.Errorf("sh-agent %s: stderr %q lacks %q", tc.args, stderr.String(), tc.stderr)
			}
		})
	}
}

// The install a rebuild or an update runs gets the home it was told to use
// and nothing else: the vendor an older manifest recorded is never forwarded,
// so the child, which has no --vendor, never refuses it (plan fyr Phase 2).
func TestInstallPassArgsForwardsTheHomeOnly(t *testing.T) {
	home := t.TempDir()
	if err := harness.SaveState(home, harness.State{Vendor: "all"}); err != nil {
		t.Fatal(err)
	}
	if got := installPassArgs("", home); len(got) != 0 {
		t.Errorf("no --home: %q, want nothing", got)
	}
	if got, want := strings.Join(installPassArgs(home, home), " "), "--home "+home; got != want {
		t.Errorf("--home: %q, want %q", got, want)
	}
}
