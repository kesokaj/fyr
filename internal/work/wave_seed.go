package work

import (
	"context"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/sandbox"
)

// waveSeed caches one wave's exported base trail records so N workers in a
// DB-only checkout run `fyr record export` once instead of N times (D17, D25).
type waveSeed struct {
	mu     sync.Mutex
	cached bool
	blob   []byte
	recs   map[string]Record
}

func (s *waveSeed) load(ctx context.Context, fyrBin, appDir, repoRoot string, homes ...string) ([]byte, map[string]Record, bool) {
	if s == nil {
		blob, recs, _ := exportSeedCandidates(ctx, fyrBin, appDir, repoRoot, homes...)
		return blob, recs, len(recs) > 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached {
		return s.blob, maps.Clone(s.recs), len(s.recs) > 0
	}
	blob, recs, cacheable := exportSeedCandidates(ctx, fyrBin, appDir, repoRoot, homes...)
	if cacheable {
		s.cached = true
		s.blob = blob
		s.recs = recs
	}
	return blob, maps.Clone(recs), len(recs) > 0
}

func exportSeedCandidates(ctx context.Context, fyrBin, appDir, repoRoot string, homes ...string) ([]byte, map[string]Record, bool) {
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
		blob, recs, ok := runOneSeedExport(ctx, fyrBin, appDir, repoRoot, h)
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

func runOneSeedExport(ctx context.Context, fyrBin, appDir, repoRoot, home string) ([]byte, map[string]Record, bool) {
	trailFile := filepath.Join(appDir, trailRel)
	defer cleanSeedTrail(trailFile)
	exp := exec.CommandContext(ctx, fyrBin, "record", "export", "--repo", repoRoot, "--home", home)
	exp.Dir = appDir
	exp.Env = sandbox.FyrEnv()
	if err := exp.Run(); err != nil {
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
