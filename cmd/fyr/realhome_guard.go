package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kesokaj/fyr/internal/harness"
)

// guardSelf is the running fyr; a test replaces it.
var guardSelf = os.Executable

// guardFree are the verbs a build that is not the home's installed fyr runs
// with no --home (ADR-505, Decision 2): install, sync and update place that
// binary, so they run from a build that is not it yet; version, list, render
// and diff open no store under the home. `help` and `--help` end before the
// guard. Every other verb, its reads and dry runs included, may open a store,
// and opening one migrates it forward (`metrics show`, `memory show`,
// `status`), so it is guarded: a list of what is free, never of what is
// guarded, so a new verb is guarded until it is named here.
var guardFree = map[string]bool{
	"install": true, "sync": true, "update": true,
	"version": true, "list": true, "render": true, "diff": true,
}

// resolveGuardedHome resolves the home, then refuses an unflagged one to a
// build that is not its installed fyr: before anything opens, the old state
// directory's migration included (BUG-507).
func (c *ctx) resolveGuardedHome() error {
	if err := c.resolveHome(); err != nil {
		return err
	}
	return c.refuseRealHomeWrite()
}

// refuseRealHomeWrite refuses an invocation with no --home when the running
// fyr is not the home's installed binary (BUG-507, ADR-505): a `go run` or a
// scratch build of a checkout, run without --home, opened the real home's
// stores and migrated metrics.db ahead of the installed release, which then
// refused it. Only the installed ~/.local/bin/fyr, compared by file
// identity, runs a guarded verb on the real home unflagged; an explicit
// --home, the real one included, is the operator's choice and passes. Exit
// 2, before the old state directory moves or any store opens.
func (c *ctx) refuseRealHomeWrite() error {
	if c.homeFlag != "" || guardFree[c.cmd] {
		return nil
	}
	installed := filepath.Join(c.home, harness.BinPath)
	self, err := guardSelf()
	if err == nil && isInstalledBinary(self, installed) {
		return nil
	}
	// The check failing is not evidence this is the installed binary: an
	// unreadable executable path, or no installed binary, refuses too.
	if self == "" {
		self = "this fyr"
	}
	fmt.Fprintf(c.out, "fyr %s: %s (build %s) is not this home's installed fyr (%s) and no --home was given; "+
		"a development build would open the real home's stores (BUG-507). "+
		"Pass --home <dir>, --home %s to mean the real one, or run the installed fyr.\n",
		c.cmd, self, buildVersion(), installed, harness.ShellArg(c.home))
	return exitCode(2)
}

// isInstalledBinary reports whether self is the installed binary by file
// identity: a link to it, or its path spelled differently, is it; a copy is
// not. Either file missing answers false.
func isInstalledBinary(self, installed string) bool {
	a, errA := os.Stat(self)
	b, errB := os.Stat(installed)
	return errA == nil && errB == nil && os.SameFile(a, b)
}
