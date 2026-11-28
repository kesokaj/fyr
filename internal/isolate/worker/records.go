package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

// maxTrail caps the trail.jsonl a branch may hold: it is agent-written and
// read whole.
const maxTrail = 256 << 20

// maxLine is one trail line's cap, fyr record's own (internal/brain's
// maxLine: a 1 MiB body escaped, and the other keys).
const maxLine = 6<<20 + 64<<10

// trailLine is one exported record as it is read: a missing key is nil.
type trailLine struct {
	Date    *string `json:"date"`
	ID      *string `json:"id"`
	Kind    *string `json:"kind"`
	Status  *string `json:"status"`
	Title   *string `json:"title"`
	Updated *string `json:"updated"`
	Body    *string `json:"body"`
}

// trailAt is the records of rev's trail.jsonl by ID; a rev without the
// file has none, and one git cannot read is an error.
func trailAt(root, rev string) (map[string]Record, error) {
	ls, err := branchOutput(leadGit(root, "ls-tree", "-z", rev, "--", trailRel))
	if err != nil {
		return nil, fmt.Errorf("at %s: git ls-tree: %v", short(rev), err)
	}
	if len(ls) == 0 {
		return map[string]Record{}, nil
	}
	meta, _, _ := strings.Cut(string(ls), "\t")
	f := strings.Fields(meta)
	if len(f) != 3 || f[1] != "blob" || (f[0] != "100644" && f[0] != "100755") {
		return nil, fmt.Errorf("at %s: %s is not a regular file", short(rev), trailRel)
	}
	cmd := blobCmd(root, f[2])
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	blob, rerr := io.ReadAll(io.LimitReader(out, maxTrail+1))
	_, _ = io.Copy(io.Discard, out)
	if err := cmd.Wait(); err != nil || rerr != nil {
		return nil, fmt.Errorf("at %s: git cat-file: %v", short(rev), errors.Join(err, rerr))
	}
	if len(blob) > maxTrail {
		return nil, fmt.Errorf("at %s: over %d MiB", short(rev), maxTrail>>20)
	}
	return parseTrail(blob)
}

func (w *worker) leadFyrBin() (string, error) {
	if p := strings.TrimSpace(w.o.Self); p != "" {
		return p, nil
	}
	exe, err := os.Executable()
	if err != nil || strings.TrimSpace(exe) == "" {
		return "", fmt.Errorf("lead fyr binary: %v", err)
	}
	return exe, nil
}

func newGroupCmd(ctx context.Context, env []string, bin, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	return cmd
}

func runSeedSync(ctx context.Context, env []string, fyrBin, appDir, sbHome string) {
	_ = runGroup(newGroupCmd(ctx, env, fyrBin, appDir, "record", "sync", "--home", sbHome), branchLimit)
}

func (w *worker) exportBoxRecords(ctx context.Context) (map[string]Record, error) {
	fyrBin, err := w.leadFyrBin()
	if err != nil {
		return nil, err
	}
	cmd := newGroupCmd(ctx, w.deps.fyrEnv(), fyrBin, w.b.App(), "record", "export", "--home", w.b.Home())
	if out, err := runBounded(cmd, branchLimit); err != nil {
		return nil, fmt.Errorf("fyr record export: %v: %s", err, strings.TrimSpace(string(out)))
	}
	f, err := os.OpenInRoot(w.b.App(), trailRel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	blob, err := io.ReadAll(io.LimitReader(f, maxTrail+1))
	if err != nil {
		return nil, err
	}
	if len(blob) > maxTrail {
		return nil, fmt.Errorf("over %d MiB", maxTrail>>20)
	}
	return parseTrail(blob)
}

// parseTrail decodes one JSON object per line with exactly the record's
// seven keys, a real date and an RFC 3339 updated.
func parseTrail(b []byte) (map[string]Record, error) {
	out := map[string]Record{}
	lines := map[string][]byte{}
	for n, l := range bytes.Split(b, []byte("\n")) {
		if len(bytes.TrimSpace(l)) == 0 {
			continue
		}
		r, err := parseLine(l)
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", n+1, err)
		}
		l = bytes.TrimRight(l, "\r")
		if first, dup := lines[r.ID]; dup {
			if !bytes.Equal(first, l) {
				return nil, fmt.Errorf("line %d: duplicate ID %s", n+1, r.ID)
			}
			continue
		}
		lines[r.ID] = l
		out[r.ID] = r
	}
	return out, nil
}

func parseLine(l []byte) (Record, error) {
	if len(l) > maxLine {
		return Record{}, fmt.Errorf("over %d bytes", maxLine)
	}
	var t trailLine
	dec := json.NewDecoder(bytes.NewReader(l))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return Record{}, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return Record{}, errors.New("more than one JSON value")
	}
	for _, k := range []struct {
		name string
		v    *string
	}{{"date", t.Date}, {"id", t.ID}, {"kind", t.Kind}, {"status", t.Status}, {"title", t.Title}, {"updated", t.Updated}, {"body", t.Body}} {
		if k.v == nil {
			return Record{}, fmt.Errorf("no %q key", k.name)
		}
	}
	r := Record{ID: *t.ID, Kind: *t.Kind, Status: *t.Status, Date: *t.Date, Title: *t.Title, Updated: *t.Updated, Body: *t.Body}
	if r.ID == "" {
		return Record{}, errors.New("no id")
	}
	if d, err := time.Parse(time.DateOnly, r.Date); err != nil || d.Format(time.DateOnly) != r.Date {
		return Record{}, fmt.Errorf("date %q is not YYYY-MM-DD", r.Date)
	}
	if _, err := time.Parse(time.RFC3339, r.Updated); err != nil {
		return Record{}, fmt.Errorf("updated %q is not RFC 3339", r.Updated)
	}
	return r, nil
}

const clockSkew = time.Minute

type window struct {
	from, to time.Time
	loc      *time.Location
}

func (w window) outside(r Record) string {
	from, to := w.from.Add(-clockSkew).Truncate(time.Second), w.to.Add(clockSkew)
	span := from.UTC().Format(time.RFC3339) + ".." + to.UTC().Format(time.RFC3339)
	u, err := time.Parse(time.RFC3339, r.Updated)
	if err != nil || u.UTC().Format(time.RFC3339) != r.Updated {
		return fmt.Sprintf("updated %.128q not in fyr record's UTC form", r.Updated)
	}
	if u.Before(from) || u.After(to) {
		return fmt.Sprintf("updated %.128q outside %s", r.Updated, span)
	}
	if w.loc == nil {
		return fmt.Sprintf("date %q not checked: the window has no zone", r.Date)
	}
	if from.In(w.loc).Format(time.DateOnly) <= r.Date && r.Date <= to.In(w.loc).Format(time.DateOnly) {
		return ""
	}
	return fmt.Sprintf("date %q not a day of %s in %s", r.Date, span, w.loc)
}

// waveSeed caches one wave's exported base trail records so N workers in a
// DB-only checkout run `fyr record export` once instead of N times (D17, D25).
type waveSeed struct {
	mu     sync.Mutex
	cached bool
	blob   []byte
	recs   map[string]Record
}

func (s *waveSeed) load(ctx context.Context, env []string, fyrBin, appDir, repoRoot string, homes ...string) ([]byte, map[string]Record, bool) {
	if s == nil {
		blob, recs, _ := exportSeedCandidates(ctx, env, fyrBin, appDir, repoRoot, homes...)
		return blob, recs, len(recs) > 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached {
		return s.blob, maps.Clone(s.recs), len(s.recs) > 0
	}
	blob, recs, cacheable := exportSeedCandidates(ctx, env, fyrBin, appDir, repoRoot, homes...)
	if cacheable {
		s.cached = true
		s.blob = blob
		s.recs = recs
	}
	return blob, maps.Clone(recs), len(recs) > 0
}

func exportSeedCandidates(ctx context.Context, env []string, fyrBin, appDir, repoRoot string, homes ...string) ([]byte, map[string]Record, bool) {
	if ctx.Err() != nil {
		return nil, nil, true
	}
	hadErr, sawCleanEmpty := false, false
	for _, h := range homes {
		if h == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(h, harness.StateDir, "brain.db")); err != nil {
			continue
		}
		blob, recs, ok := runOneSeedExport(ctx, env, fyrBin, appDir, repoRoot, h)
		if !ok {
			if ctx.Err() != nil {
				return nil, nil, true
			}
			hadErr = true
			continue
		}
		if len(recs) > 0 {
			return blob, recs, true
		}
		sawCleanEmpty = true
	}
	return nil, nil, !hadErr && sawCleanEmpty
}

func runOneSeedExport(ctx context.Context, env []string, fyrBin, appDir, repoRoot, home string) ([]byte, map[string]Record, bool) {
	trailFile := filepath.Join(appDir, trailRel)
	defer cleanSeedTrail(trailFile)
	exp := newGroupCmd(ctx, env, fyrBin, appDir, "record", "export", "--repo", repoRoot, "--home", home)
	if _, err := runBounded(exp, branchLimit); err != nil {
		return nil, nil, false
	}
	f, err := os.Open(trailFile)
	if err != nil {
		return nil, nil, false
	}
	blob, rerr := io.ReadAll(io.LimitReader(f, maxTrail+1))
	_ = f.Close()
	if rerr != nil || len(blob) > maxTrail {
		return nil, nil, false
	}
	recs, err := parseTrail(blob)
	if err != nil {
		return nil, nil, false
	}
	return blob, recs, true
}

func cleanSeedTrail(trailFile string) {
	_ = os.Remove(trailFile)
	_ = os.Remove(filepath.Dir(trailFile))
}
