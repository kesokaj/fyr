// Command fyr installs the agent harness (global context, skills,
// subagents, commands and rules) into the Antigravity CLI and keeps its
// records. Its help is help_text.go: `fyr` or `fyr help` prints the short
// page, `fyr help <verb>` or `fyr <verb> --help` the verb's page.
//
//	fyr install     deploy the harness into ~/.gemini/config (alias: sync)
//	fyr update      move this place to the newest harness
//	fyr status      what is installed, and whether it drifted
//	fyr diff        the diff of every drifted file
//	fyr uninstall   remove exactly what install wrote
//	fyr list        the skills and agents
//	fyr render      print the assembled context
//	fyr adopt       copy a skill from elsewhere into skills/
//	fyr record      add, get, edit, list or import trail records
//	fyr memory      save or show this project's memory
//	fyr search      ranked search of records and files
//	fyr index       reconcile the search index; --register opts in
//	fyr dream       the nightly dreamer and its timer
//	fyr metrics     session shapes: ingest, show, report, export
//	fyr eval        run behavioural rows against the CLI
//	fyr check       read-only drift check of this checkout's docs
//	fyr version     print the build version
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
)

// stderr is where a read-only verb names the state directory from before the
// rename, and where `record add` and `record edit` print the redactor
// warning: their stdout is data (render, list, metrics show --json, a new
// record's ID); a test replaces it.
var stderr io.Writer = os.Stderr

// version is set by a release build, -ldflags "-X main.version=vX.Y.Z"; empty
// otherwise, and buildVersion reads the build info instead.
var version string

func main() {
	if os.Getenv(harness.ChildEnv) != "1" {
		oldEnvNotices(os.Environ(), os.Stderr)
	}
	if err := run(os.Args[1:], os.Stdout); err != nil {
		var code exitCode
		if errors.As(err, &code) {
			os.Exit(int(code))
		}
		fmt.Fprintln(os.Stderr, "fyr:", err)
		os.Exit(1)
	}
}

// oldEnvNotices names each SH_AGENT_* variable still set: fyr reads FYR_*
// only, and an old SH_AGENT_REPO ignored in silence would change which tree
// an install takes (plan fyr D15). An empty one changes nothing and is not
// named; the rebuilt child of an install inherits the parent's environment
// and stays quiet, so each variable is named once.
func oldEnvNotices(environ []string, w io.Writer) {
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		if name, ok := strings.CutPrefix(k, "SH_AGENT_"); ok && v != "" {
			fmt.Fprintf(w, "fyr: $%s is set but no longer read; rename it $FYR_%s\n", k, name)
		}
	}
}
