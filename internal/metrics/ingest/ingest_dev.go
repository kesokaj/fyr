package ingest

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
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// EncodeWaveStamp prefixes a wave stamp with a length-framed label so labelled
// dev-home waves coexist in work_waves without schema changes or collisions.
func EncodeWaveStamp(label, rawStamp string) string {
	_, raw := DecodeWaveStamp(strings.TrimSpace(rawStamp))
	label = strings.TrimSpace(label)
	if label == "" {
		return raw
	}
	return fmt.Sprintf("%d:%s:%s", len(label), label, raw)
}

// DecodeWaveStamp splits an encoded wave stamp into its label and raw stamp;
// unlabelled stamps return label == "".
func DecodeWaveStamp(stored string) (label, raw string) {
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

// WorkRunKey returns a bounded run key scoped to a wave stamp and worker name.
func WorkRunKey(rawStamp, worker string) string {
	_, raw := DecodeWaveStamp(rawStamp)
	return ScopedKey("work", raw+"/"+strings.TrimSpace(worker))
}

// ScopedKey builds a printable, length-bounded composite key with prefix.
func ScopedKey(prefix, val string) string {
	prefix = search.Printable(strings.TrimSpace(prefix))
	val = search.Printable(strings.TrimSpace(val))
	if val == "" {
		return ""
	}
	joined := prefix + ":" + val
	if len(joined) <= trialTextLimit {
		return joined
	}
	sum := sha256.Sum256([]byte(prefix + "\x00" + val))
	if len(prefix) > 40 {
		prefix = trail.TruncateUTF8(prefix, 40)
	}
	return fmt.Sprintf("%s:%x", prefix, sum[:16])
}

func (d Deps) ingestDevHome(o Options, r *Result, log io.Writer) {
	if d.DB == nil || o.Source == "" || o.Label == "" || o.Run != "" {
		return
	}
	d.IngestWorkWavesWithLabel(o.Source, o.Label, log)
	d.ingestDevWorkerSandboxes(o, r, log)
	d.ingestDevMetricsStore(o, log)
}

func (d Deps) ingestDevWorkerSandboxes(o Options, r *Result, log io.Writer) {
	sbRoot := filepath.Join(o.Source, harness.StateDir, "sandbox")
	for _, proj := range readRegularSubdirs(sbRoot, log) {
		d.ingestProjectWorkerSandboxes(filepath.Join(sbRoot, proj), o, r, log)
	}
}

func (d Deps) ingestProjectWorkerSandboxes(projDir string, o Options, r *Result, log io.Writer) {
	for _, stamp := range readRegularSubdirs(projDir, log) {
		stampDir := filepath.Join(projDir, stamp)
		for _, worker := range readRegularSubdirs(stampDir, log) {
			workerHome := filepath.Join(stampDir, worker, "home")
			if !isRegularDir(workerHome) || !isRegularDir(filepath.Join(workerHome, ".gemini", "antigravity-cli")) {
				continue
			}
			d.ingestAgy(Options{
				Home:   o.Home,
				Source: workerHome,
				Label:  o.Label,
				Run:    WorkRunKey(stamp, worker),
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

func (d Deps) ingestDevMetricsStore(o Options, log io.Writer) {
	srcPath := metricsStorePath(o.Source)
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
	if sameRegularFile(d.StorePath, srcPath) {
		return
	}
	tmp, err := os.MkdirTemp(filepath.Dir(d.StorePath), "metrics-agy-")
	if err != nil {
		noteSecondBrain(log, "dev metrics temp dir: %v", err)
		return
	}
	defer os.RemoveAll(tmp)
	d.CopyDevStoreTables(srcPath, tmp, o.Label, log)
}
