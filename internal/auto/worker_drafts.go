package auto

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/isolate"
)

// collectAutoDrafts copies auto-drafts.jsonl from the worker's DraftsDir into
// ~/.config/fyr/auto/drafts/<name>.jsonl (D26 S1) and returns any D39 finding
// lines for the project's report.
func (r *run) collectAutoDrafts(root, name, class string, res isolate.WorkerResult) ([]Finding, error) {
	if res.Drafts == 0 || res.DraftsDir == "" {
		return nil, nil
	}
	rel := filepath.Join(filepath.FromSlash(res.DraftsDir), "auto-drafts.jsonl")
	lead, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer lead.Close()
	f, err := lead.OpenFile(rel, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, errors.New("auto-drafts.jsonl unreadable or over 1 MiB")
	}
	if err := ensureSubdir(r.o.Home, "drafts"); err != nil {
		return nil, err
	}
	dest := filepath.Join(dir(r.o.Home), "drafts", name+".jsonl")
	if err := harness.WriteAtomic(dest, data, 0o600); err != nil {
		return nil, err
	}
	return r.processDraftLines(root, class, data), nil
}

// cleanProjectDrafts removes the .agents/scratch/work/<stamp>/<name> directory
// work.Run created in root, and empty parents up to .agents/scratch/work.
func cleanProjectDrafts(root, draftsDir string) {
	if draftsDir == "" {
		return
	}
	lead, err := os.OpenRoot(root)
	if err != nil {
		return
	}
	defer lead.Close()
	rel := filepath.Clean(filepath.FromSlash(draftsDir))
	_ = lead.Remove(filepath.Join(rel, "auto-drafts.jsonl"))
	for _, dir := range []string{rel, filepath.Dir(rel), filepath.Join(".agents", "scratch", "work"), filepath.Join(".agents", "scratch")} {
		_ = lead.Remove(dir)
	}
}

// verifyAutoDirs re-checks auto/ and its subdirectories after a worker so a
// directory replaced by a symlink during a host run is refused before any
// post-worker write (D26 S5).
func verifyAutoDirs(home string) error {
	if err := ensureDir(home); err != nil {
		return err
	}
	for _, sub := range []string{"kickoffs", "drafts", "inputs"} {
		p := filepath.Join(dir(home), sub)
		info, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", p)
		}
	}
	return nil
}

func configTampered(home string, before []byte, beforeErr error) bool {
	after, afterErr := readBounded(ConfigPath(home), configLimit)
	if (beforeErr != nil) != (afterErr != nil) {
		return true
	}
	return !bytes.Equal(before, after)
}

func (r *run) handleConfigTamper(branch, runnerWhy string) (string, []Finding) {
	var warns []string
	if err := disableConfig(r.o.Home, r.cfg); err != nil {
		warns = append(warns, "could not disable config: "+oneLine(err.Error()))
	}
	if err := saveState(r.o.Home, r.st); err != nil {
		warns = append(warns, "could not save state: "+oneLine(err.Error()))
	}
	r.failed, r.halted = true, true
	base := "auto disabled: its config changed during a run"
	if len(warns) > 0 {
		base += " (warning: " + strings.Join(warns, "; ") + ")"
	}
	return base, []Finding{{"worker", "config-changed", branch, withRunner(base, runnerWhy)}}
}

func disableConfig(home string, c Config) error {
	if c.ConversationIdle == "" {
		c = defaults()
	}
	c.Enabled = false
	return save(home, c)
}

func fileSHA256(path string) (string, error) {
	data, err := readBounded(path, stateLimit)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum), nil
}
