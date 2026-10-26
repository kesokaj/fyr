package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

// updateFromZip installs the harness in the repository zip the operator
// downloaded, with the running binary: unpack, install, delete the unpacked
// copy (R8, R9's zip place; ADR-038). Nothing is fetched, and nothing in the
// zip replaces the binary: the running one is installed as in every install.
func updateFromZip(zipPath, home string, dryRun bool, out io.Writer) (err error) {
	// A dry run installs nothing and never creates ~/.config/fyr: when that
	// directory is absent the unpack goes into a zip-* directory beside the
	// zip instead, never the system temp directory (IN10), and that directory
	// is removed before this returns, as on a real run; the usage says so
	// (IN21).
	parent := filepath.Join(home, harness.StateDir)
	if dryRun {
		if _, err := os.Stat(parent); err != nil {
			parent = ""
		}
	} else {
		// Created here, and removed again if anything below fails — the unpack
		// or the install that can still refuse the zip's content — so a
		// refused zip leaves the home as it was (BUG-093).
		// `=`, not `:=`: the deferred check must see the function's result, not
		// a block-scoped copy of it.
		var created []string
		if created, err = harness.MkdirAllTracked(parent); err != nil {
			return err
		}
		defer func() {
			if err != nil {
				harness.RemoveCreated(created)
			}
		}()
	}
	// The zip place is the fallback for machines that cannot take the normal
	// way in, so it accepts the zip it is given (ADR-041). A release zip with
	// its SHA256SUMS beside it is checked before a byte is written, and refused
	// if that check fails; without it, it is installed marked UNVERIFIED (R9,
	// ADR-053: the checksums are not signed, so the check is of integrity).
	verifiedTag := ""
	check := func(sum string) error {
		tag, err := harness.VerifyZipSums(zipPath, sum)
		if errors.Is(err, harness.ErrZipNoSums) {
			return nil
		}
		if err != nil {
			return err
		}
		verifiedTag = tag
		return nil
	}
	dir, sum, err := harness.UnpackZip(zipPath, parent, check)
	if err != nil {
		return err
	}
	defer func() {
		if rerr := os.RemoveAll(dir); rerr != nil {
			fmt.Fprintf(out, "warning: could not remove the unpacked copy %s: %v\n", dir, rerr)
		}
	}()
	prefix := ""
	if dryRun {
		prefix = "[dry-run] "
	}
	trust := "UNVERIFIED — no SHA256SUMS beside it; trust rests on your own download"
	if verifiedTag != "" {
		trust = "CHECKED against the SHA256SUMS of release " + verifiedTag + " (integrity only)"
	}
	fmt.Fprintf(out, "%szip %s (sha256 %s, %s): installing its harness and the running binary %s; nothing is downloaded and nothing in the zip replaces the binary\n", prefix, zipPath, sum, trust, buildVersion())
	// The manifest keeps the verdict too, so status can tell a verified install
	// from an unverified one after the output has scrolled away.
	src := harness.Source{FS: os.DirFS(dir), Dir: dir, Origin: harness.ZipOrigin(sum, verifiedTag)}
	if err := installFiles(src, home, dryRun, out); err != nil {
		return err
	}
	// Only after a successful install: a refused or dry run changes nothing,
	// not even a stale leftover from an interrupted run.
	if !dryRun {
		harness.SweepStaleDirs(parent, "zip-", time.Hour, out)
	}
	return nil
}
