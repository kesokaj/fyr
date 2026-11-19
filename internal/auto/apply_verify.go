package auto

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/check"
	"github.com/kesokaj/fyr/internal/isolate"
)

// parseAutoTests extracts command lines under `## Setup commands` in AGENTS.md
// that sit inside a fenced code block and end with `# auto: test`.
func parseAutoTests(agentsMD string) []string {
	var cmds []string
	inSetup, fence := false, ""
	for _, raw := range strings.Split(agentsMD, "\n") {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if fence == "" {
			switch {
			case strings.HasPrefix(line, "## "):
				inSetup = (line == "## Setup commands")
			case strings.HasPrefix(line, "# "):
				inSetup = false
			case inSetup && (strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~")):
				fence = line[:3]
			}
			continue
		}
		if strings.HasPrefix(line, fence) {
			fence = ""
			continue
		}
		if rawCmd, ok := strings.CutSuffix(line, "# auto: test"); ok {
			cmd := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rawCmd), "$ "))
			if cmd != "" && !strings.HasPrefix(cmd, "#") {
				cmds = append(cmds, cmd)
			}
		}
	}
	return cmds
}

func (r *run) verifyWorktree(root, wtDir, name, runner string, tests []string) error {
	wtTestDir := filepath.Join(dir(r.o.Home), "wt-test-"+name)
	if r.o.TestRunner == nil {
		_ = os.RemoveAll(wtTestDir)
		for _, sub := range []string{"home", "tmp"} {
			if err := os.MkdirAll(filepath.Join(wtTestDir, sub), 0o700); err != nil {
				return fmt.Errorf("marked test scratch dir: %w", err)
			}
		}
		defer os.RemoveAll(wtTestDir)
	}
	runTest := r.o.testRunner(r.context(), root, wtTestDir)
	for _, cmdLine := range tests {
		if err := runTest(wtDir, runner, cmdLine); err != nil {
			return fmt.Errorf("marked test %q: %w", cmdLine, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(wtDir, "PRD.md")); err == nil {
		rep, cerr := check.Run(wtDir, r.o.Vocabulary, r.o.Home)
		if cerr != nil {
			return fmt.Errorf("fyr check: %w", cerr)
		}
		if len(rep.Findings) > 0 {
			return fmt.Errorf("fyr check: %d findings", len(rep.Findings))
		}
	}
	return nil
}

func (o Options) testRunner(ctx context.Context, root, wtTestDir string) func(wtDir, runner, cmdLine string) error {
	if o.TestRunner != nil {
		return o.TestRunner
	}
	return func(wtDir, runner, cmdLine string) error {
		return runMarkedTest(ctx, o, root, wtDir, wtTestDir, runner, cmdLine)
	}
}

func runMarkedTest(ctx context.Context, o Options, root, wtDir, wtTestDir, _ /* runner */, cmdLine string) error {
	var ro []string
	if root != "" {
		ro = append(ro, filepath.Join(root, ".git"))
	}
	ro = append(ro, o.TestRO...)
	opts := isolate.CommandOpts{
		Dir:         wtDir,
		ScratchHome: filepath.Join(wtTestDir, "home"),
		TmpDir:      filepath.Join(wtTestDir, "tmp"),
		RealHome:    o.realHome(),
		Argv:        []string{"bash", "-c", cmdLine},
		ROPaths:     ro,
		RWPaths:     o.TestRW,
		Unconfined:  o.TestUnconfined,
	}
	return runShellTest(ctx, opts, o.limit(workerTimeout))
}

func runShellTest(ctx context.Context, opts isolate.CommandOpts, timeout time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd, cleanup, err := isolate.Command(opts)
	if err != nil {
		return err
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	} else {
		cmd.SysProcAttr.Setsid = true
	}
	cmd.WaitDelay = time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		cleanup()
		return err
	}
	cleanup()
	if err := waitShellTest(tctx, cmd, timeout); err != nil {
		return fmt.Errorf("%v: %s", err, oneLine(out.String()))
	}
	return nil
}

func waitShellTest(ctx context.Context, cmd *exec.Cmd, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		err := <-done
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("timed out after %s", timeout)
		}
		if err != nil {
			return err
		}
		return ctx.Err()
	}
}

// verifyBranchForApply checks that refs/heads/<branch> descends from base with
// at least one commit and touches no rubric-denied path, instruction file,
// symlink, submodule or denied trail record.
func verifyBranchForApply(root, base, branch, class string) error {
	ref := "refs/heads/" + branch
	tip, err := applyGit(root, nil, "rev-parse", "--verify", "--end-of-options", ref)
	if err != nil || tip == "" {
		return fmt.Errorf("missing branch %s", branch)
	}
	if _, err := applyGit(root, nil, "merge-base", "--is-ancestor", "--end-of-options", base, ref); err != nil {
		return fmt.Errorf("branch %s does not descend from base", branch)
	}
	countStr, err := applyGit(root, nil, "rev-list", "--count", "--end-of-options", base+".."+ref, "--")
	if n, nerr := strconv.Atoi(countStr); err != nil || nerr != nil || n < 1 {
		return fmt.Errorf("branch %s has no commits past base", branch)
	}
	rawDiff, err := applyGitBytes(root, nil, "diff", "--raw", "-z", "--no-renames", "--end-of-options", base, ref, "--")
	if err != nil {
		return err
	}
	return checkRawDiff(root, base, ref, class, rawDiff)
}

func checkRawDiff(root, base, ref, class string, rawDiff []byte) error {
	spec, _ := lookupClass(class)
	parts := strings.Split(string(rawDiff), "\x00")
	trailChanged := false
	for i := 0; i+1 < len(parts); i += 2 {
		meta, path := parts[i], parts[i+1]
		if meta == "" || path == "" {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) >= 5 && fields[4] == "D" {
			return fmt.Errorf("branch deletes file %s", path)
		}
		if len(fields) >= 2 && (fields[1] == "120000" || fields[1] == "160000") {
			return fmt.Errorf("branch adds symlink or submodule %s", path)
		}
		if Denied(path) || (!spec.Instructions && Instruction(path)) {
			return fmt.Errorf("branch touches rubric-denied path %s", path)
		}
		trailChanged = trailChanged || path == ".agents/trail.jsonl"
	}
	if trailChanged {
		return verifyTrailForApply(root, base, ref)
	}
	return nil
}

func verifyTrailForApply(root, base, ref string) error {
	baseTrail, err := readGitBlob(root, base, ".agents/trail.jsonl")
	if err != nil {
		return fmt.Errorf("cannot read base trail: %w", err)
	}
	refTrail, err := applyGitBytes(root, nil, "show", "--end-of-options", ref+":.agents/trail.jsonl")
	if err != nil {
		return fmt.Errorf("cannot read branch trail: %w", err)
	}
	var baseRecs []brain.Record
	if len(bytes.TrimSpace(baseTrail)) > 0 {
		if baseRecs, err = brain.ParseTrail(bytes.NewReader(baseTrail)); err != nil {
			return fmt.Errorf("invalid base trail: %w", err)
		}
	}
	refRecs, err := brain.ParseTrail(bytes.NewReader(refTrail))
	if err != nil {
		return fmt.Errorf("invalid trail record on branch: %w", err)
	}
	baseByID := make(map[string]brain.Record, len(baseRecs))
	for _, r := range baseRecs {
		baseByID[r.ID] = r
	}
	refByID := make(map[string]brain.Record, len(refRecs))
	for _, r := range refRecs {
		refByID[r.ID] = r
	}
	for _, b := range baseRecs {
		if _, ok := refByID[b.ID]; !ok {
			return fmt.Errorf("branch deletes record %s", b.ID)
		}
	}
	for _, r := range refRecs {
		if b, ok := baseByID[r.ID]; (!ok || r != b) && (deniedTrailRecord(r) || (ok && deniedTrailRecord(b))) {
			return fmt.Errorf("branch touches rubric-denied record %s", r.ID)
		}
	}
	return nil
}

func deniedTrailRecord(r brain.Record) bool {
	return Denied(r.ID) || strings.EqualFold(r.Kind, "adr") || (strings.EqualFold(r.Kind, "plan") && strings.Contains(r.Body, deniedBox))
}
