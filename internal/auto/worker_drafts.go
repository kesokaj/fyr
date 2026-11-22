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
	"github.com/kesokaj/fyr/internal/search"
)

// collectAutoDrafts copies auto-drafts.jsonl from the worker's DraftsDir into
// ~/.config/fyr/auto/drafts/<name>.jsonl (D26 S1), returning any D39 finding
// lines and the optional validated sim_manifest for eval-gate runs.
func (r *run) collectAutoDrafts(root, name, class string, res isolate.WorkerResult) ([]Finding, *simManifest, error) {
	if res.Drafts == 0 || res.DraftsDir == "" {
		return nil, nil, nil
	}
	baseRoot, rel, ok, err := resolveDraftsLocation(r.o.Home, root, res.DraftsDir)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, nil
	}
	defer baseRoot.Close()
	f, err := baseRoot.OpenFile(filepath.Join(rel, "auto-drafts.jsonl"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, nil, errors.New("auto-drafts.jsonl unreadable or over 1 MiB")
	}
	if err := ensureSubdir(r.o.Home, "drafts"); err != nil {
		return nil, nil, err
	}
	dest := filepath.Join(dir(r.o.Home), "drafts", name+".jsonl")
	if err := harness.WriteAtomic(dest, data, 0o600); err != nil {
		return nil, nil, err
	}
	fs, manifest := r.processDraftLines(root, class, data)
	return fs, manifest, nil
}

func resolveDraftsLocation(home, root, draftsDir string) (*os.Root, string, bool, error) {
	if filepath.IsAbs(draftsDir) {
		sbRoot := isolate.Root(home, "sandbox")
		rel, err := filepath.Rel(sbRoot, filepath.Clean(draftsDir))
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, "", false, fmt.Errorf("drafts dir %s outside sandbox root", draftsDir)
		}
		r, err := os.OpenRoot(sbRoot)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, "", false, nil
		}
		if err != nil {
			return nil, "", false, err
		}
		return r, rel, true, nil
	}
	if root == "" || root == search.GlobalWorkspace {
		return nil, "", false, nil
	}
	lead, err := os.OpenRoot(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", false, nil
	}
	if err != nil {
		return nil, "", false, err
	}
	return lead, filepath.Clean(filepath.FromSlash(draftsDir)), true, nil
}

func (r *run) cleanWorkerDrafts(root, draftsDir string) {
	if draftsDir == "" {
		return
	}
	if filepath.IsAbs(draftsDir) {
		sbRoot := isolate.Root(r.o.Home, "sandbox")
		rel, err := filepath.Rel(sbRoot, filepath.Clean(draftsDir))
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return
		}
		if sr, err := os.OpenRoot(sbRoot); err == nil {
			_ = sr.Remove(filepath.Join(rel, "auto-drafts.jsonl"))
			for _, d := range []string{rel, filepath.Dir(rel), "drafts"} {
				_ = sr.Remove(d)
			}
			sr.Close()
		}
		_ = os.Remove(sbRoot)
		return
	}
	cleanProjectDrafts(root, draftsDir)
}

// cleanProjectDrafts removes the .agents/scratch/work/<stamp>/<name> directory
// work.Run created in root, and empty parents up to .agents/scratch/work.
func cleanProjectDrafts(root, draftsDir string) {
	if draftsDir == "" || root == "" || root == search.GlobalWorkspace {
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
