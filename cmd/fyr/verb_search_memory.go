package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/search"
)

// runMemory needs no harness tree (R10).
func runMemory(c *ctx) error {
	return memoryCmd(c.fs.Args(), brain.MemoryEntry{Type: c.memType, Name: c.memName, Description: c.memDesc}, c.home, c.out)
}

// runSearch serves `search` and `index`; neither needs a harness tree, and a
// release-binary or zip place has none (plan D10).
func runSearch(c *ctx) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	o := search.Options{Dir: wd, Home: c.home, Words: c.fs.Args(), Refs: c.refs, Open: c.openRecords,
		Kind: c.kind, Limit: c.limit, Global: c.global, Rebuild: c.rebuild}
	if c.cmd == "index" && c.register {
		return runRegister(c, o)
	} else if c.cmd == "index" {
		err = search.Index(o, c.out)
	} else if err = search.Validate(o); err == nil {
		recordHits(c, o)
		err = search.Search(o, c.out)
	}
	if search.IsUsage(err) {
		fmt.Fprintln(c.out, "fyr", err)
		return exitCode(2)
	}
	return err
}

// stdin is where `memory add` reads the body; a test replaces it.
var stdin io.Reader = os.Stdin

// memorySubverbs is what memoryCmd accepts; the check vocabulary reads it,
// and a test pins it against the dispatcher (plan fyr-check D14).
var memorySubverbs = []string{"add", "show"}

// memoryCmd runs `memory add` and `memory show` in the checkout holding the
// working directory: an alias over the brain store's memory records (D14).
// A refusal prints its reason and exits 2.
func memoryCmd(args []string, e brain.MemoryEntry, home string, out io.Writer) error {
	if len(args) != 1 || !slices.Contains(memorySubverbs, args[0]) {
		// A usage error exits 2 like every other usage error here (dream's, a verb's unknown flag).
		fmt.Fprintln(out, "usage: fyr memory add --type T --name SLUG --description TEXT (body on stdin) | fyr memory show")
		return exitCode(2)
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	o := brain.Options{Home: home, Dir: wd}
	var text string
	if args[0] == "add" {
		text, err = brain.MemoryAdd(o, e, stdin)
		if err == nil {
			text = "saved " + text + "\n"
		}
	} else {
		text, err = brain.MemoryShow(o)
	}
	if brain.IsRefusal(err) || brain.IsUsage(err) {
		fmt.Fprintln(out, search.Printable("fyr memory: "+err.Error())) // on the caller's writer, where a test reads it (IN13)
		return exitCode(2)
	}
	if err != nil {
		return errors.New(search.Printable(err.Error()))
	}
	fmt.Fprint(out, text)
	return nil
}
