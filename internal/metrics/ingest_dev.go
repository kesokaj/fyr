package metrics

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
)

// encodeWaveStamp prefixes a wave stamp with a length-framed label so labelled
// dev-home waves coexist in work_waves without schema changes or collisions.
func encodeWaveStamp(label, rawStamp string) string {
	_, raw := decodeWaveStamp(strings.TrimSpace(rawStamp))
	label = strings.TrimSpace(label)
	if label == "" {
		return raw
	}
	return fmt.Sprintf("%d:%s:%s", len(label), label, raw)
}

// decodeWaveStamp splits an encoded wave stamp into its label and raw stamp;
// unlabelled stamps return label == "".
func decodeWaveStamp(stored string) (label, raw string) {
	nStr, rest, ok := strings.Cut(stored, ":")
	if !ok || nStr == "" {
		return "", stored
	}
	n, err := strconv.Atoi(nStr)
	if err != nil || n <= 0 || n > 128 || len(rest) <= n || rest[n] != ':' {
		return "", stored
	}
	cand := rest[:n]
	if !hasASCIIAlpha(cand) || rest[n+1:] == "" {
		return "", stored
	}
	return cand, rest[n+1:]
}

func hasASCIIAlpha(s string) bool {
	for i := 0; i < len(s); i++ {
		if (s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= 'a' && s[i] <= 'z') {
			return true
		}
	}
	return false
}

func waveMatchesLabel(stamp string, o ShowOptions) bool {
	lbl, _ := decodeWaveStamp(stamp)
	if o.Label != "" {
		return lbl == o.Label
	}
	if o.AllLabels {
		return true
	}
	return lbl == ""
}

func workRunKey(rawStamp, worker string) string {
	_, raw := decodeWaveStamp(rawStamp)
	return scopedKey("work", raw+"/"+strings.TrimSpace(worker))
}

func scopedKey(prefix, val string) string {
	prefix = printable(strings.TrimSpace(prefix))
	val = printable(strings.TrimSpace(val))
	if val == "" {
		return ""
	}
	if prefix == "" {
		return capRunText(val)
	}
	joined := prefix + ":" + val
	if len(joined) <= trialText {
		return joined
	}
	sum := sha256.Sum256([]byte(prefix + "\x00" + val))
	if len(prefix) > 40 {
		prefix = prefix[:40]
	}
	return fmt.Sprintf("%s:%x", prefix, sum[:16])
}

func capRunText(val string) string {
	if len(val) <= trialText {
		return val
	}
	sum := sha256.Sum256([]byte(val))
	return fmt.Sprintf("%s:%x", val[:40], sum[:16])
}

// ingestDevHome ingests labelled work waves, worker sandboxes, and dev-home
// metrics.db trials/sessions under o.Label without mixing into unlabelled rows.
func ingestDevHome(s *Store, o Options, r *Result, log io.Writer) {
	if s == nil || s.db == nil || o.Source == "" || o.Label == "" || o.Run != "" {
		return
	}
	s.ingestWorkWavesWithLabel(o.Source, o.Label, log)
	s.ingestDevWorkerSandboxes(o, r, log)
	s.ingestDevMetricsStore(o, log)
}

func (s *Store) ingestDevWorkerSandboxes(o Options, r *Result, log io.Writer) {
	sbRoot := filepath.Join(o.Source, harness.StateDir, "sandbox")
	for _, proj := range readRegularSubdirs(sbRoot, log) {
		s.ingestProjectWorkerSandboxes(filepath.Join(sbRoot, proj), o, r, log)
	}
}

func (s *Store) ingestProjectWorkerSandboxes(projDir string, o Options, r *Result, log io.Writer) {
	for _, stamp := range readRegularSubdirs(projDir, log) {
		stampDir := filepath.Join(projDir, stamp)
		for _, worker := range readRegularSubdirs(stampDir, log) {
			workerHome := filepath.Join(stampDir, worker, "home")
			if !isRegularDir(workerHome) || !isRegularDir(filepath.Join(workerHome, ".gemini", "antigravity-cli")) {
				continue
			}
			s.ingestAgy(Options{
				Home:   o.Home,
				Source: workerHome,
				Label:  o.Label,
				Run:    workRunKey(stamp, worker),
			}, r, log)
		}
	}
}

func readRegularSubdirs(dir string, log io.Writer) []string {
	fi, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		noteSecondBrain(log, "%s: %v", dir, err)
		return nil
	}
	if !fi.IsDir() {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		noteSecondBrain(log, "%s: %v", dir, err)
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.Type().IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

func isRegularDir(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.IsDir()
}

func sameRegularFile(a, b string) bool {
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

func (s *Store) ingestDevMetricsStore(o Options, log io.Writer) {
	srcPath := Path(o.Source)
	fi, err := os.Lstat(srcPath)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		noteSecondBrain(log, "%s: %v", srcPath, err)
		return
	}
	if !fi.Mode().IsRegular() {
		noteSecondBrain(log, "%s: not a regular file", srcPath)
		return
	}
	if sameRegularFile(s.path, srcPath) {
		return
	}
	tmp, err := os.MkdirTemp(filepath.Dir(s.path), "metrics-agy-")
	if err != nil {
		noteSecondBrain(log, "dev metrics temp dir: %v", err)
		return
	}
	defer os.RemoveAll(tmp)
	s.copyDevStoreTables(srcPath, tmp, o.Label, log)
}
