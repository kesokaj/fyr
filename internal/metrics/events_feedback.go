package metrics

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kesokaj/fyr/internal/harness"
)

// maxFeedbackLine is the longest feedback.jsonl line read.
const maxFeedbackLine = 4 * 1024 * 1024

// readFeedback is the feedback log's entries by project root: their times,
// and the entry's ts as its reference. Nothing else is taken from a line.
// The file is opened as every ingest read is (openRegular), and a line over
// the cap ends the read with a note naming it, never silently (SD58 of the
// 2026-10-27 audit).
func readFeedback(home string) (map[string][]stamped, string) {
	out := map[string][]stamped{}
	f, _, err := openRegular(filepath.Join(home, harness.StateDir, "feedback.jsonl"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, ""
		}
		return out, "feedback.jsonl: " + err.Error()
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), maxFeedbackLine)
	n := 0
	for sc.Scan() {
		n++
		var e struct {
			TS      string `json:"ts"`
			Project string `json:"project"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Project == "" {
			continue
		}
		if ts, ref, ok := correctionRef(e.TS); ok {
			out[filepath.Clean(e.Project)] = append(out[filepath.Clean(e.Project)], stamped{ts: ts, ref: ref})
		}
	}
	if err := sc.Err(); errors.Is(err, bufio.ErrTooLong) {
		return out, fmt.Sprintf("feedback.jsonl: line %d over %d MiB, the rest skipped", n+1, maxFeedbackLine>>20)
	} else if err != nil {
		return out, "feedback.jsonl: " + err.Error()
	}
	return out, ""
}
