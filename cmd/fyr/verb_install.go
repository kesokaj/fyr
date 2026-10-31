package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/kesokaj/fyr/internal/harness"
)

func runInstall(c *ctx) error {
	repo := c.repo()
	if repo == "" {
		// A release binary installs the harness built into it and itself:
		// there is no source to build from (D13).
		if c.dryRun {
			fmt.Fprintf(c.out, "[dry-run] release binary %s: the harness built into it and the running binary would be installed\n", c.src.Version)
		} else {
			fmt.Fprintf(c.out, "release binary %s: installing the harness built into it and the running binary\n", c.src.Version)
		}
		return installFiles(c.src, c.home, c.dryRun, c.out)
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
	if c.dryRun && rebuild {
		fmt.Fprintf(c.out, "[dry-run] the binary would be rebuilt from %s\n", repo)
	} else if rebuild {
		return harness.Reinstall(repo, installPassArgs(c.homeFlag, c.home), c.out)
	}
	if !own && c.dryRun {
		fmt.Fprintln(c.out, "[dry-run] plain copy, not its own git checkout: the running binary would be installed as is")
	} else if !own {
		fmt.Fprintln(c.out, "plain copy, not its own git checkout: the running binary is installed as is")
	} else if goErr != nil {
		fmt.Fprintln(c.out, "warning: go is not on PATH; the running binary was copied, not rebuilt — install Go and run `fyr update`")
	}
	return installFiles(c.src, c.home, c.dryRun, c.out)
}

func runDiff(c *ctx) error {
	// A zip install is not compared: the zip is not kept, and the source this
	// binary resolves (its embedded files, a checkout) is not what was
	// installed (ADR-038).
	if st, found, err := harness.LoadState(c.home); err == nil && found && st.Origin().Kind == harness.OriginZip {
		fmt.Fprintln(c.out, "diff       not compared — this home was installed from a zip that is not kept; "+zipAdvice())
		return nil
	}
	files, err := harness.Plan(c.src, nil)
	if err != nil {
		return err
	}
	return harness.Diff(c.home, files, c.out)
}

func runList(c *ctx) error {
	inv, err := harness.LoadInventory(c.src)
	if err != nil {
		return err
	}
	fmt.Fprint(c.out, harness.RenderInventory(inv))
	return nil
}

func runRender(c *ctx) error {
	inv, err := harness.LoadInventory(c.src)
	if err != nil {
		return err
	}
	data, err := harness.RenderContext(c.src, inv)
	if err != nil {
		return err
	}
	_, err = c.out.Write(data)
	return err
}

func runAdopt(c *ctx) error {
	if c.fs.NArg() != 1 {
		return usageErr(c.out, "adopt", "")
	}
	repo := c.repo()
	if repo == "" {
		return errors.New("adopt needs a harness checkout (`git clone git@github.com:kesokaj/fyr.git`, then run adopt from a build of it); this install is a release binary and carries no tree to adopt into")
	}
	dest, err := harness.Adopt(repo, c.home, c.fs.Arg(0), c.force)
	if err != nil {
		return err
	}
	next := "commit and push the repo"
	if !harness.IsCheckout(repo) {
		// A plain copy has no repository: the adopted skill lives only here
		// until it is copied into a checkout.
		next = "copy it into your harness checkout and commit there — this tree is a plain copy with no repository"
	}
	fmt.Fprintf(c.out, "adopted into %s\nnext: review it, run `fyr install`, then %s\n", dest, next)
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
