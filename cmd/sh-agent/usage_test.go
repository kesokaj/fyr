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
		"dream --vendor bogus":             {[]string{"dream", "--all", "--vendor", "bogus", "--home", home}, "unknown vendor"},
		"memory show --vendor bogus":       {[]string{"memory", "show", "--vendor", "bogus", "--home", home}, "unknown vendor"},
		"search without words":             {[]string{"search", "--home", home}, "give words"},
		"search --limit -1":                {[]string{"search", "--limit", "-1", "--home", home, "x"}, "--limit must be"},
		"adopt without a name":             {[]string{"adopt", "--repo", "../..", "--home", home}, "usage: sh-agent adopt"},
		"update --from-zip without a file": {[]string{"update", "--from-zip", "--dry-run", "--home", home}, "--from-zip needs a file"},
		"install --from-zip":               {[]string{"install", "--from-zip", "x.zip", "--home", home}, "--from-zip belongs to update"},
		// --vendor is every verb's flag, checked once before the verb runs:
		// status exited 1 and uninstall and report took any word (BUG-171).
		"status --vendor bogus":              {[]string{"status", "--repo", "../..", "--vendor", "bogus", "--home", home}, "unknown vendor"},
		"uninstall --dry-run --vendor bogus": {[]string{"uninstall", "--dry-run", "--vendor", "bogus", "--home", home}, "unknown vendor"},
		"report --dry-run --vendor bogus":    {[]string{"report", "--dry-run", "--vendor", "bogus", "--home", home}, "unknown vendor"},
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

// An explicit `--vendor all` reaches the install a rebuild or an update runs,
// so it overrides a narrower vendor the home's state recorded; the shared
// pass-through dropped it (review of the 2026-10-09 fixes, round 1).
func TestInstallPassArgsForwardsAnExplicitVendor(t *testing.T) {
	home := t.TempDir()
	if err := harness.SaveState(home, harness.State{Vendor: harness.VendorClaude}); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		vendorFlag string
		want       string
	}{
		"explicit all":   {"all", "--vendor all"},
		"explicit agy":   {"agy", "--vendor antigravity"},
		"from the state": {"", "--vendor claude"},
	} {
		t.Run(name, func(t *testing.T) {
			args, err := installPassArgs("", home, tc.vendorFlag)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(args, " "); got != tc.want {
				t.Errorf("installPassArgs(vendor %q) = %q, want %q", tc.vendorFlag, got, tc.want)
			}
		})
	}
}
