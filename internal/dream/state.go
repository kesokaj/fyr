package dream

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

// state is ~/.config/fyr/dream-state.json: each project's watermark and
// the last run (D9; never in search.db, which is a cache).
type state struct {
	Projects map[string]time.Time `json:"projects"`
	LastRun  lastRun              `json:"last_run"`
}

type lastRun struct {
	At        time.Time `json:"at"`
	Projects  int       `json:"projects"`
	Proposals int       `json:"proposals"`
	Error     string    `json:"error,omitempty"`
}

func statePath(home string) string {
	return filepath.Join(home, harness.StateDir, "dream-state.json")
}

// readState reads the state. A missing file is a first run; a file that
// cannot be read or parsed is an error, never a silent reset of every
// watermark, which would re-send two weeks of every project (code review
// 2026-09-30).
func readState(home string) (state, error) {
	st := state{Projects: map[string]time.Time{}}
	data, err := os.ReadFile(statePath(home))
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, fmt.Errorf("reading %s: %w", statePath(home), err)
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return st, fmt.Errorf("%s is not valid JSON (%v): fix or remove it; removing it re-reads the last 14 days of every project", statePath(home), err)
	}
	if st.Projects == nil {
		st.Projects = map[string]time.Time{}
	}
	return st, nil
}
