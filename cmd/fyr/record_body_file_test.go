package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestRecordBodyFileAndOut tests --body-file on record add and edit, and
// --out on record get: regular-file and scratch-path guards, symlink and FIFO
// refusals, piped-stdin refusal, size and redactor parity with stdin, and
// round-trip editing without the get header line.
func TestRecordBodyFileAndOut(t *testing.T) {
	root := recordRepo(t)
	home := t.TempDir()
	dir := t.TempDir()
	writeFile := func(name, text string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	body1 := "# Plan\n\n- [ ] first box\n"
	good := writeFile("plan.md", body1)
	id, err := recordCmd(t, "", "record", "add", "plan", "--id", "PLAN-2026-11-03-speed", "--title", "Speed plan", "--body-file", good, "--home", home)
	if err != nil || strings.TrimSpace(id) != "PLAN-2026-11-03-speed" {
		t.Fatalf("add --body-file: %v %q", err, id)
	}

	// record get --out writes the body only, creating subdirectories under
	// .agents/scratch/, and round-trips through record edit --body-file.
	relOut := ".agents/scratch/sub/plan.md"
	if out, err := recordCmd(t, "", "record", "get", "PLAN-2026-11-03-speed", "--out", relOut, "--home", home); err != nil || out != "" {
		t.Fatalf("get --out relative: %v %q", err, out)
	}
	gotBytes, err := os.ReadFile(filepath.Join(root, relOut))
	if err != nil {
		t.Fatalf("read --out file: %v", err)
	}
	if string(gotBytes) != body1 {
		t.Fatalf("--out wrote %q, want body only %q", string(gotBytes), body1)
	}

	// Absolute path under the checkout's .agents/scratch/ also works (even on
	// macOS where root is /var/... and git's toplevel is /private/var/...).
	absOut := filepath.Join(root, ".agents", "scratch", "abs-plan.md")
	edited := strings.Replace(string(gotBytes), "- [ ] first box", "- [x] first box", 1)
	if err := os.WriteFile(filepath.Join(root, relOut), []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := recordCmd(t, "", "record", "edit", "PLAN-2026-11-03-speed", "--body-file", filepath.Join(root, relOut), "--home", home); err != nil {
		t.Fatalf("edit --body-file: %v %q", err, out)
	}
	if out, err := recordCmd(t, "", "record", "get", "PLAN-2026-11-03-speed", "--out", absOut, "--home", home); err != nil || out != "" {
		t.Fatalf("get --out absolute: %v %q", err, out)
	}
	if b, err := os.ReadFile(absOut); err != nil || string(b) != edited {
		t.Fatalf("after edit --body-file, get --out = %q (%v), want %q", string(b), err, edited)
	}
	oldFi, err := os.Lstat(absOut)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := recordCmd(t, "", "record", "get", "PLAN-2026-11-03-speed", "--out", absOut, "--home", home); err != nil || out != "" {
		t.Fatalf("get --out overwrite: %v %q", err, out)
	}
	if newFi, err := os.Lstat(absOut); err != nil || os.SameFile(oldFi, newFi) {
		t.Errorf("get --out overwrite reused the old inode instead of Remove + O_CREATE|O_EXCL: err=%v", err)
	}

	// Redactor warning parity with stdin on both add and edit.
	var warnBuf bytes.Buffer
	oldStderr := stderr
	stderr = &warnBuf
	defer func() { stderr = oldStderr }()
	secretFile := writeFile("secret.md", "line one\n-----BEGIN OPENSSH PRIVATE KEY-----\n")
	if out, err := recordCmd(t, "", "record", "add", "change", "--title", "c", "--body-file", secretFile, "--home", home); err != nil {
		t.Fatalf("add with redactable body-file: %v %q", err, out)
	}
	if !strings.Contains(warnBuf.String(), "first at line 2") {
		t.Errorf("add --body-file did not warn on redactable body: %q", warnBuf.String())
	}
	warnBuf.Reset()
	if out, err := recordCmd(t, "", "record", "edit", "PLAN-2026-11-03-speed", "--body-file", secretFile, "--home", home); err != nil {
		t.Fatalf("edit with redactable body-file: %v %q", err, out)
	}
	if !strings.Contains(warnBuf.String(), "first at line 2") {
		t.Errorf("edit --body-file did not warn on redactable body: %q", warnBuf.String())
	}

	// Refusals for --body-file on add and edit: symlink, directory, FIFO,
	// missing file, empty file, over 1 MiB, NUL byte, control char, piped stdin,
	// and --body + --body-file together on edit.
	linkPath := filepath.Join(dir, "link.md")
	if err := os.Symlink(good, linkPath); err != nil {
		t.Fatal(err)
	}
	fifoPath := filepath.Join(dir, "fifo.md")
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]string{
		"symlink":   linkPath,
		"directory": dir,
		"fifo":      fifoPath,
		"missing":   filepath.Join(dir, "missing.md"),
		"empty":     writeFile("empty.md", "  \n"),
		"over 1MiB": writeFile("big.md", strings.Repeat("a", (1<<20)+1)),
		"nul byte":  writeFile("nul.md", "hello\x00world\n"),
		"ctrl char": writeFile("ctrl.md", "hello\x07world\n"),
		"empty arg": "",
	} {
		if out, err := recordCmd(t, "", "record", "add", "bug", "--title", "t", "--body-file", p, "--home", home); err == nil {
			t.Errorf("add --body-file %s: succeeded with %q", name, out)
		}
		if out, err := recordCmd(t, "", "record", "edit", "PLAN-2026-11-03-speed", "--body-file", p, "--home", home); err == nil {
			t.Errorf("edit --body-file %s: succeeded with %q", name, out)
		}
	}
	if out, err := recordCmd(t, "piped stdin\n", "record", "add", "bug", "--title", "t", "--body-file", good, "--home", home); exitOf(err) != 1 || !strings.Contains(out, "piped stdin") {
		t.Errorf("add --body-file with piped stdin: exit %d %q", exitOf(err), out)
	}
	if out, err := recordCmd(t, "piped stdin\n", "record", "edit", "PLAN-2026-11-03-speed", "--body-file", good, "--home", home); exitOf(err) != 1 || !strings.Contains(out, "piped stdin") {
		t.Errorf("edit --body-file with piped stdin: exit %d %q", exitOf(err), out)
	}
	// Redirected regular file on stdin (even empty) is also refused with --body-file.
	emptyRedirect := writeFile("redirect.md", "")
	rf, err := os.Open(emptyRedirect)
	if err != nil {
		t.Fatal(err)
	}
	defer rf.Close()
	stdin = rf
	var redirectOut bytes.Buffer
	if rerr := run([]string{"record", "add", "bug", "--title", "t", "--body-file", good, "--home", home}, &redirectOut); exitOf(rerr) != 1 || !strings.Contains(redirectOut.String(), "piped stdin") {
		t.Errorf("add --body-file with redirected file stdin: exit %d %q", exitOf(rerr), redirectOut.String())
	}
	stdin = os.Stdin

	// An open pipe on stdin with no bytes written must not block --body-file.
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	doneTimer := make(chan struct{})
	go func() {
		select {
		case <-time.After(100 * time.Millisecond):
			pw.Close()
		case <-doneTimer:
			pw.Close()
		}
	}()
	stdin = pr
	startPipe := time.Now()
	var pipeOut bytes.Buffer
	pipeErr := run([]string{"record", "add", "change", "--title", "idle-pipe", "--body-file", good, "--home", home}, &pipeOut)
	close(doneTimer)
	stdin = os.Stdin
	if pipeErr != nil || time.Since(startPipe) >= 80*time.Millisecond {
		t.Errorf("add --body-file with idle open pipe on stdin: err=%v elapsed=%v out=%q", pipeErr, time.Since(startPipe), pipeOut.String())
	}

	if out, err := recordCmd(t, "", "record", "edit", "PLAN-2026-11-03-speed", "--body", "--body-file", good, "--home", home); exitOf(err) != 2 || !strings.Contains(out, "not both") {
		t.Errorf("edit --body and --body-file: exit %d %q", exitOf(err), out)
	}
	if out, _ := recordCmd(t, "", "record", "list", "--kind", "bug", "--home", home); strings.TrimSpace(out) != "" {
		t.Errorf("refused add stored a bug record: %q", out)
	}

	// Refusals for record get --out: multiple IDs, missing ID (creates no file),
	// paths outside .agents/scratch/, .. traversal, and symlinks at every level.
	if out, err := recordCmd(t, "", "record", "get", "PLAN-2026-11-03-speed", "CHANGE-001", "--out", ".agents/scratch/two.md", "--home", home); exitOf(err) != 2 {
		t.Errorf("get --out with two IDs: exit %d %q", exitOf(err), out)
	}
	noSuchOut := filepath.Join(root, ".agents", "scratch", "nosuch.md")
	if out, err := recordCmd(t, "", "record", "get", "BUG-404", "--out", ".agents/scratch/nosuch.md", "--home", home); exitOf(err) != 1 {
		t.Errorf("get --out missing ID: exit %d %q", exitOf(err), out)
	}
	if _, err := os.Lstat(noSuchOut); !os.IsNotExist(err) {
		t.Errorf("get --out on missing ID created %s: %v", noSuchOut, err)
	}

	scratchLink := filepath.Join(root, ".agents", "scratch", "link-out.md")
	if err := os.Symlink(good, scratchLink); err != nil {
		t.Fatal(err)
	}
	scratchLinkDir := filepath.Join(root, ".agents", "scratch", "linkdir")
	if err := os.Symlink(dir, scratchLinkDir); err != nil {
		t.Fatal(err)
	}
	outsideSecret := writeFile("outside-secret.txt", "do not overwrite\n")
	scratchHardlink := filepath.Join(root, ".agents", "scratch", "hardlink.md")
	if err := os.Link(outsideSecret, scratchHardlink); err != nil {
		t.Fatal(err)
	}
	for name, badOut := range map[string]string{
		"empty":           "",
		"repo root":       "outside.md",
		"dot-agents":      ".agents/outside.md",
		"scratch root":    ".agents/scratch",
		"trailing slash":  ".agents/scratch/sub/",
		"dotdot":          ".agents/scratch/../outside.md",
		"deep dotdot":     ".agents/scratch/sub/../../outside.md",
		"other abs dir":   filepath.Join(dir, ".agents", "scratch", "out.md"),
		"symlink target":  ".agents/scratch/link-out.md",
		"symlink subdir":  ".agents/scratch/linkdir/out.md",
		"hardlink target": ".agents/scratch/hardlink.md",
		"existing subdir": ".agents/scratch/sub",
	} {
		if out, err := recordCmd(t, "", "record", "get", "PLAN-2026-11-03-speed", "--out", badOut, "--home", home); err == nil {
			t.Errorf("get --out %s (%q): succeeded with %q", name, badOut, out)
		}
	}
	if b, err := os.ReadFile(outsideSecret); err != nil || string(b) != "do not overwrite\n" {
		t.Errorf("hardlink target was modified: %q (%v)", string(b), err)
	}
	if helpOut, err := recordCmd(t, "", "help", "record"); err != nil || !strings.Contains(helpOut, "(provenance P) on memory and feedback") {
		t.Errorf("help record missing provenance note: %v %q", err, helpOut)
	}

	// Symlinked .agents/scratch itself is refused.
	if err := os.RemoveAll(filepath.Join(root, ".agents", "scratch")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, filepath.Join(root, ".agents", "scratch")); err != nil {
		t.Fatal(err)
	}
	if out, err := recordCmd(t, "", "record", "get", "PLAN-2026-11-03-speed", "--out", ".agents/scratch/escaped.md", "--home", home); exitOf(err) != 1 {
		t.Errorf("get --out through symlinked .agents/scratch: exit %d %q", exitOf(err), out)
	}
	if _, err := os.Lstat(filepath.Join(dir, "escaped.md")); !os.IsNotExist(err) {
		t.Errorf("get --out wrote through symlinked .agents/scratch")
	}
}

// TestMemoryAddBodyFile tests --body-file on fyr memory add and its refusals on
// memory add and memory show.
func TestMemoryAddBodyFile(t *testing.T) {
	recordRepo(t)
	home := t.TempDir()
	dir := t.TempDir()
	bodyFile := filepath.Join(dir, "mem.md")
	if err := os.WriteFile(bodyFile, []byte("Integration tests use the real database.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := recordCmd(t, "", "memory", "add", "--type", "feedback", "--name", "no-db-mocks", "--description", "integration tests hit the real database", "--body-file", bodyFile, "--home", home)
	if err != nil || out != "saved MEMORY-no-db-mocks\n" {
		t.Fatalf("memory add --body-file: %v %q", err, out)
	}
	show, err := recordCmd(t, "", "memory", "show", "--home", home)
	if err != nil || !strings.Contains(show, "no-db-mocks (feedback)") {
		t.Fatalf("memory show: %v %q", err, show)
	}

	linkFile := filepath.Join(dir, "link.md")
	if err := os.Symlink(bodyFile, linkFile); err != nil {
		t.Fatal(err)
	}
	if out, err := recordCmd(t, "", "memory", "add", "--type", "feedback", "--name", "bad-link", "--description", "d", "--body-file", linkFile, "--home", home); exitOf(err) != 2 {
		t.Errorf("memory add symlink --body-file: exit %d %q", exitOf(err), out)
	}
	if out, err := recordCmd(t, "piped\n", "memory", "add", "--type", "feedback", "--name", "bad-pipe", "--description", "d", "--body-file", bodyFile, "--home", home); exitOf(err) != 2 || !strings.Contains(out, "piped stdin") {
		t.Errorf("memory add piped stdin + --body-file: exit %d %q", exitOf(err), out)
	}
	if out, err := recordCmd(t, "", "memory", "show", "--body-file", bodyFile, "--home", home); exitOf(err) != 2 {
		t.Errorf("memory show --body-file: exit %d %q", exitOf(err), out)
	}
	if out, err := recordCmd(t, "", "memory", "bogus", "--body-file", filepath.Join(dir, "missing.md"), "--home", home); exitOf(err) != 2 || !strings.Contains(out, "usage: fyr memory") {
		t.Errorf("memory bogus --body-file: exit %d %q, want usage error before opening file", exitOf(err), out)
	}
}

// TestNoRedirectedFyrInstructions pins Item 5 of PLAN-2026-11-02-1806-fyr-speed
// and BUG-501: no harness instruction redirects a file or heredoc into fyr
// (which breaks the unsandboxed(fyr) prefix grant and raises a permission
// dialog), none tells the caller to strip the header line of fyr record get
// instead of using --out, and handoff's record add passes --id so the Kickoff
// names the stored handoff ID.
func TestNoRedirectedFyrInstructions(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	redirectRe := regexp.MustCompile("`[^`]*\\bfyr\\s+(?:record|memory)\\b[^`]*(?:\\s<\\s|<<)[^`]*`|^\\s*fyr\\s+(?:record|memory)\\b.*(?:\\s<\\s|<<)")
	bareBodyRe := regexp.MustCompile(`--body(?:[^-]|$)`)
	for _, root := range []string{"context", "agents", "skills", "rules", "commands"} {
		err := filepath.WalkDir(filepath.Join(repo, root), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && (d.Name() == "catalog" || d.Name() == "grilling") {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(path, ".md") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(repo, path)
			for i, line := range strings.Split(string(data), "\n") {
				if redirectRe.MatchString(line) {
					t.Errorf("%s:%d redirects input into fyr instead of --body-file: %s", rel, i+1, strings.TrimSpace(line))
				}
				if bareBodyRe.MatchString(line) {
					t.Errorf("%s:%d uses bare --body (stdin pipe) instead of --body-file: %s", rel, i+1, strings.TrimSpace(line))
				}
				if strings.Contains(line, "drop the header line") || strings.Contains(line, "header line dropped") || strings.Contains(line, "drop the first line, the record's header") {
					t.Errorf("%s:%d tells the caller to drop the header line instead of fyr record get --out: %s", rel, i+1, strings.TrimSpace(line))
				}
				if strings.Contains(line, "the body on stdin") {
					t.Errorf("%s:%d tells the caller to pass the body on stdin instead of --body-file: %s", rel, i+1, strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	handoffBytes, err := os.ReadFile(filepath.Join(repo, "skills", "handoff", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	handoff := string(handoffBytes)
	if !strings.Contains(handoff, "fyr record add handoff --id HANDOFF-<stamp>-<slug>") || !strings.Contains(handoff, "--body-file .agents/scratch/handoff-<stamp>.md") {
		t.Errorf("skills/handoff/SKILL.md does not store the handoff with --id HANDOFF-<stamp>-<slug> and --body-file (BUG-501)")
	}
	specBytes, err := os.ReadFile(filepath.Join(repo, "skills", "spec", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(specBytes), "[a-z0-9-]") {
		t.Errorf("skills/spec/SKILL.md does not constrain <app> and <topic> to [a-z0-9-]")
	}
}
