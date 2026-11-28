package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
)

func runInstall(c *runtimeCtx) error {
	o := dryRunJSONOptsOf(c)
	if !o.jsonOut {
		return runInstallBody(c)
	}
	out, err := captureVerbOutput(c, func() error { return runInstallBody(c) })
	if err != nil {
		return err
	}
	return writeVerbJSON(c.out, installJSON{Verb: c.cmd, OK: true, DryRun: o.dryRun, Home: c.home, Output: out})
}

func runInstallBody(c *runtimeCtx) error {
	dryRun := dryRunJSONOptsOf(c).dryRun
	repo := c.repo()
	if repo == "" {
		// A release binary installs the harness built into it and itself:
		// there is no source to build from (D13).
		if dryRun {
			fmt.Fprintf(c.out, "[dry-run] release binary %s: the harness built into it and the running binary would be installed\n", c.src.Version)
		} else {
			fmt.Fprintf(c.out, "release binary %s: installing the harness built into it and the running binary\n", c.src.Version)
		}
		return installFiles(c.src, c.home, dryRun, c.out)
	}
	if err := knownCheckout(c.repoFlag, c.home, repo); err != nil {
		return err
	}
	// The deployed binary must be the repo's own build: rebuild and let that
	// build install, unless this process is that build (ChildEnv), there is
	// no toolchain, the tree is a plain copy, or this is a dry run — a dry run
	// never builds or runs a fresh binary, though like every install it reads
	// the checkout's git state, which is why the checkout must be one the
	// operator named or installed before. A plain copy (no .git) never
	// rebuilds: whoever unpacked it built the running binary from that same
	// tree, the tree may be read-only, and git cannot update it (BUG-015).
	_, goErr := exec.LookPath("go")
	own := harness.IsCheckout(repo)
	rebuild := shouldRebuild(goErr == nil, os.Getenv(harness.ChildEnv) == "1", own)
	if dryRun && rebuild {
		fmt.Fprintf(c.out, "[dry-run] the binary would be rebuilt from %s\n", repo)
	} else if rebuild {
		return harness.Reinstall(repo, installPassArgs(c.homeFlag, c.home), c.out)
	}
	if !own && dryRun {
		fmt.Fprintln(c.out, "[dry-run] plain copy, not its own git checkout: the running binary would be installed as is")
	} else if !own {
		fmt.Fprintln(c.out, "plain copy, not its own git checkout: the running binary is installed as is")
	} else if goErr != nil && dryRun {
		fmt.Fprintln(c.out, "[dry-run] warning: go is not on PATH; the running binary would be copied, not rebuilt — install Go and run `fyr update`")
	} else if goErr != nil {
		fmt.Fprintln(c.out, "warning: go is not on PATH; the running binary was copied, not rebuilt — install Go and run `fyr update`")
	}
	return installFiles(c.src, c.home, dryRun, c.out)
}

func runDiff(c *runtimeCtx) error {
	jsonOut := jsonOptsOf(c).jsonOut
	// A zip install is not compared: the zip is not kept, and the source this
	// binary resolves (its embedded files, a checkout) is not what was
	// installed (ADR-038).
	if st, found, err := harness.LoadState(c.home); err == nil && found && st.Origin().Kind == harness.OriginZip {
		msg := "diff       not compared — this home was installed from a zip that is not kept; " + zipAdvice() + "\n"
		if jsonOut {
			return writeVerbJSON(c.out, diffJSON{OK: true, Drift: false, Output: msg})
		}
		fmt.Fprint(c.out, msg)
		return nil
	}
	files, err := harness.Plan(c.src, nil)
	if err != nil {
		return err
	}
	if !jsonOut {
		return harness.Diff(c.home, files, c.out)
	}
	var buf bytes.Buffer
	err = harness.Diff(c.home, files, &buf)
	var code exitCode
	if err != nil && !errors.As(err, &code) {
		return err
	}
	out := buf.String()
	drift := err != nil || strings.TrimSpace(out) != ""
	if jerr := writeVerbJSON(c.out, diffJSON{OK: err == nil, Drift: drift, Output: out}); jerr != nil {
		return jerr
	}
	return err
}

func runList(c *runtimeCtx) error {
	inv, err := harness.LoadInventory(c.src)
	if err != nil {
		return err
	}
	out := harness.RenderInventory(inv)
	if jsonOptsOf(c).jsonOut {
		return writeVerbJSON(c.out, listJSON{Source: verbSourceLabel(c.src), Output: out})
	}
	fmt.Fprint(c.out, out)
	return nil
}

func runRender(c *runtimeCtx) error {
	data, err := harness.RenderContext(c.src)
	if err != nil {
		return err
	}
	if jsonOptsOf(c).jsonOut {
		return writeVerbJSON(c.out, renderJSON{Source: verbSourceLabel(c.src), Bytes: len(data), Content: string(data)})
	}
	_, err = c.out.Write(data)
	return err
}

func runAdopt(c *runtimeCtx) error {
	o := adoptOptsOf(c)
	if c.fs.NArg() != 1 {
		return usageErr(c.out, "adopt", "")
	}
	repo := c.repo()
	if repo == "" {
		return errors.New("adopt needs a harness checkout (`git clone git@github.com:kesokaj/fyr.git`, then run adopt from a build of it); this install is a release binary and carries no tree to adopt into")
	}
	dest, err := harness.Adopt(repo, c.home, c.fs.Arg(0), o.force)
	if err != nil {
		return err
	}
	next := "commit and push the repo"
	if !harness.IsCheckout(repo) {
		// A plain copy has no repository: the adopted skill lives only here
		// until it is copied into a checkout.
		next = "copy it into your harness checkout and commit there — this tree is a plain copy with no repository"
	}
	out := fmt.Sprintf("adopted into %s\nnext: review it, run `fyr install`, then %s\n", dest, next)
	if o.jsonOut {
		return writeVerbJSON(c.out, adoptJSON{OK: true, Force: o.force, Args: c.fs.Args(), Output: out})
	}
	fmt.Fprint(c.out, out)
	return nil
}

// installFiles plans the source with the running binary and installs it,
// then the PATH note. No vendor setting is read or written (ADR-062): the
// harness is files in the customization directory, nothing else.
func installFiles(src harness.Source, home string, dryRun bool, out io.Writer) error {
	self, err := selfExecutable()
	if err != nil {
		return err
	}
	files, err := harness.Plan(src, self)
	if err != nil {
		return err
	}
	if err := harness.Install(src, home, files, dryRun, out); err != nil {
		return err
	}
	// A dry run wrote no binary, so no statement about how PATH reaches it is
	// true.
	if !dryRun {
		pathHint(home, out)
	}
	return nil
}
