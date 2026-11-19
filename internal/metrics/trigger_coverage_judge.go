package metrics

import (
	"path/filepath"
	"strings"
	"time"
)

type waveJudgeEntry struct {
	proj, stamp, rawStamp, worker, verdict string
	matched, fallbackCounted               bool
}

func buildWaveJudgeEntries(waves []waveRow) []waveJudgeEntry {
	var out []waveJudgeEntry
	for _, w := range waves {
		jv := normJudgeVerdict(w.judgeVerdict)
		if jv == "" {
			continue
		}
		_, raw := decodeWaveStamp(w.stamp)
		proj := ""
		if p := strings.TrimSpace(w.projectRoot); p != "" {
			proj = filepath.Clean(p)
		}
		out = append(out, waveJudgeEntry{
			proj: proj, stamp: strings.TrimSpace(w.stamp), rawStamp: raw,
			worker: strings.TrimSpace(w.worker), verdict: jv,
			fallbackCounted: sameModelFamily(w.model, w.judgeModel),
		})
	}
	return out
}

func normJudgeVerdict(raw string) string {
	v := strings.ToUpper(strings.TrimSpace(raw))
	switch {
	case v == "PASS":
		return "PASS"
	case strings.Contains(v, "FAIL"):
		return "FAIL"
	default:
		return ""
	}
}

func matchWaveJudgeEntry(entries []waveJudgeEntry, r JudgeVerdictRow, verdict string) int {
	target := strings.TrimSpace(r.Target)
	if target == "" || verdict == "" {
		return -1
	}
	rProj := ""
	if p := strings.TrimSpace(r.ProjectRoot); p != "" {
		rProj = filepath.Clean(p)
	}
	for i := range entries {
		e := &entries[i]
		if e.matched || e.verdict != verdict {
			continue
		}
		if e.proj != "" && rProj != "" && e.proj != rProj {
			continue
		}
		if waveTargetMatches(target, *e) {
			return i
		}
	}
	return -1
}

func waveTargetMatches(target string, e waveJudgeEntry) bool {
	if target == e.stamp || (e.rawStamp != "" && target == e.rawStamp) {
		return true
	}
	if e.worker == "" {
		return false
	}
	return target == e.stamp+"/"+e.worker || (e.rawStamp != "" && target == e.rawStamp+"/"+e.worker) || (e.stamp != "" && target == e.worker)
}

func (s *Store) populateJudgeVerdictsHealth(sum *Summary, o ShowOptions, waves []waveRow, since, now time.Time) error {
	sum.Health.JudgePassed, sum.Health.JudgeFailed, sum.Health.JudgeFallback =
		sum.F.JudgePassed, sum.F.JudgeFailed, sum.F.JudgeFallback
	if o.Label != "" {
		return nil
	}
	rows, err := s.JudgeVerdicts(o.Project, since, now)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil
		}
		return err
	}
	entries := buildWaveJudgeEntries(waves)
	for _, r := range rows {
		applyJudgeVerdictRow(sum, entries, r)
	}
	return nil
}

func applyJudgeVerdictRow(sum *Summary, entries []waveJudgeEntry, r JudgeVerdictRow) {
	v := normJudgeVerdict(r.Verdict)
	isFallback := strings.TrimSpace(r.FallbackReason) != ""
	if idx := matchWaveJudgeEntry(entries, r, v); idx >= 0 {
		entries[idx].matched = true
		if isFallback && !entries[idx].fallbackCounted {
			entries[idx].fallbackCounted = true
			sum.Health.JudgeFallback++
			sum.F.JudgeFallback++
		}
		return
	}
	switch v {
	case "PASS":
		sum.Health.JudgePassed++
		sum.F.JudgePassed++
		sum.TriggerCoverage.JudgePassed++
	case "FAIL":
		sum.Health.JudgeFailed++
		sum.F.JudgeFailed++
		sum.TriggerCoverage.JudgeFailed++
	}
	if isFallback {
		sum.Health.JudgeFallback++
		sum.F.JudgeFallback++
	}
}
