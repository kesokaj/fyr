package metrics

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics/report"
)

// TriggerCoverage aggregates the autonomy and feature-use KPIs computed from metrics.db.
type TriggerCoverage = report.TriggerCoverage

type installedSkillMeta struct {
	name        string
	isInterview bool
	isSwarm     bool
}

type sessionCommitSummary struct {
	hasNonAmbigCommit bool
	hasHandoff        bool
	firstCommitMS     int64
}

func populateTriggerCoverage(sum *Summary, o ShowOptions, all []sess, trials []trial, data *showData, since, now time.Time) {
	var tc TriggerCoverage
	var rawWeeks float64
	if d := now.Sub(since); d > 0 {
		rawWeeks = d.Hours() / (24 * 7)
		tc.WindowWeeks = round4(rawWeeks)
	}
	addWaveTriggerCoverageRows(&tc, data.waves)
	for _, tr := range trials {
		if strings.HasPrefix(tr.label, "auto=") {
			tc.AutoModelRuns++
		}
	}
	if rawWeeks > 0 && tc.AutoModelRuns > 0 {
		tc.AutoModelRunsPerWeek = round4(float64(tc.AutoModelRuns) / rawWeeks)
	}
	catalog := discoverInstalledSkills(o.Home)
	interviewSkills := catalogInterviewSkills(catalog)
	swarmSkills := catalogSwarmSkills(catalog)
	addSessionTriggerCoverage(&tc, all, data.steps, data.events, interviewSkills, swarmSkills)
	populateSkillCatalogCoverage(&tc, catalog, sum.C.Skills["agy"])
	tc.SearchCalls = sum.C.SQLiteVerbs["agy"]
	tc.GrepCalls = sum.C.GrepCalls["agy"]
	if tc.GrepCalls > 0 {
		r := round4(float64(tc.SearchCalls) / float64(tc.GrepCalls))
		tc.SearchGrepRatio = &r
	}
	tc.ReadLedgerBlocks = sum.E.LedgerSaved
	tc.PreInvocationTurnVets = sum.E.TurnVets
	tc.PreInvocationTimedOut = sum.E.TimedOutTurns
	tc.PreInvocationTimeoutPct = sum.E.TimeoutRatePct
	tc.PreInvocationP50MS = sum.E.P50DurationMS
	tc.PreInvocationP95MS = sum.E.P95DurationMS
	finalizeTriggerCoverageRates(&tc)
	sum.TriggerCoverage = tc
}

func discoverInstalledSkills(home string) []installedSkillMeta {
	if strings.TrimSpace(home) == "" {
		return nil
	}
	skillsDir := filepath.Join(home, harness.AgyRoot, "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil
	}
	var out []installedSkillMeta
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		meta, ok := readInstalledSkillMeta(filepath.Join(skillsDir, e.Name(), "SKILL.md"), e.Name())
		if ok {
			out = append(out, meta)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func readInstalledSkillMeta(path, fallbackName string) (installedSkillMeta, bool) {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return installedSkillMeta{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return installedSkillMeta{}, false
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 128*1024))
	if err != nil {
		return installedSkillMeta{}, false
	}
	text := string(raw)
	name := parseSkillFrontmatterName(text)
	if name == "" {
		name = fallbackName
	}
	name = printable(strings.TrimSpace(name))
	if name == "" {
		return installedSkillMeta{}, false
	}
	low := strings.ToLower(text)
	lowName := strings.ToLower(name)
	isInterview := strings.Contains(low, "pre-build interview") || strings.Contains(low, "ask_question") || lowName == "grilling" || lowName == "fyr-feature"
	isSwarm := strings.Contains(low, "run_workflow") || strings.Contains(low, "/fyr-swarm") || strings.Contains(lowName, "swarm")
	return installedSkillMeta{name: name, isInterview: isInterview, isSwarm: isSwarm}, true
}

func parseSkillFrontmatterName(text string) string {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			break
		}
		if k, v, ok := strings.Cut(trimmed, ":"); ok && strings.TrimSpace(k) == "name" {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

func catalogInterviewSkills(catalog []installedSkillMeta) map[string]bool {
	out := map[string]bool{}
	for _, sk := range catalog {
		if sk.isInterview {
			out[sk.name] = true
		}
	}
	if len(catalog) == 0 {
		out["grilling"] = true
		out["fyr-feature"] = true
	}
	return out
}

func catalogSwarmSkills(catalog []installedSkillMeta) map[string]bool {
	out := map[string]bool{}
	for _, sk := range catalog {
		if sk.isSwarm {
			out[sk.name] = true
		}
	}
	if len(catalog) == 0 {
		out["fyr-swarm"] = true
	}
	return out
}

func populateSkillCatalogCoverage(tc *TriggerCoverage, catalog []installedSkillMeta, loaded map[string]int) {
	if len(catalog) == 0 {
		return
	}
	tc.InstalledSkillsCount = len(catalog)
	var untriggered []string
	for _, sk := range catalog {
		if loaded[sk.name] > 0 {
			tc.TriggeredSkillsCount++
		} else {
			untriggered = append(untriggered, sk.name)
		}
	}
	pct := round4(float64(tc.TriggeredSkillsCount) * 100.0 / float64(tc.InstalledSkillsCount))
	tc.SkillCoveragePct = &pct
	tc.UntriggeredSkills = untriggered
}

func finalizeTriggerCoverageRates(tc *TriggerCoverage) {
	if tc.QualifyingPhases > 0 {
		pct := round4(float64(tc.WorkerPhases) * 100 / float64(tc.QualifyingPhases))
		tc.QualifyingWorkerPhasePct = &pct
	}
	if tc.WorkerPhases > 0 {
		pct := round4(float64(tc.ConfiguredWorkerPhases) * 100 / float64(tc.WorkerPhases))
		tc.ConfiguredWorkerPhasePct = &pct
	}
	if tc.BuildSessions > 0 {
		pct := round4(float64(tc.DoDClosedSessions) * 100 / float64(tc.BuildSessions))
		tc.DoDClosedPct = &pct
		gatePct := round4(float64(tc.DeterministicGateSessions) * 100 / float64(tc.BuildSessions))
		tc.DeterministicGatePct = &gatePct
	}
}

func addWaveTriggerCoverageRows(tc *TriggerCoverage, waves []waveRow) {
	for _, w := range waves {
		recordWaveCoverageRow(tc, w)
	}
}

func recordWaveCoverageRow(tc *TriggerCoverage, w waveRow) {
	if strings.HasPrefix(strings.TrimSpace(w.worker), "auto-") {
		tc.AutoModelRuns++
		return
	}
	tc.WorkerPhases++
	m := strings.TrimSpace(w.model)
	bcTrim := strings.TrimSpace(w.baseCheck)
	jmTrim := strings.TrimSpace(w.judgeModel)
	jvTrim := strings.TrimSpace(w.judgeVerdict)
	if m != "" && !strings.EqualFold(m, "default") && bcTrim != "" && (jmTrim != "" || jvTrim != "") {
		tc.ConfiguredWorkerPhases++
	}
	var ws WorkSummary
	addWaveWorkerCounts(&ws, w.state, w.model, w.baseCheck, w.judgeModel, w.judgeVerdict)
	tc.WorkerPassed += ws.Passed
	tc.BaseCheckPassed += ws.BaseCheckPassed
	tc.BaseCheckFailed += ws.BaseCheckFailed
	tc.JudgePassed += ws.JudgePassed
	tc.JudgeFailed += ws.JudgeFailed
}

func addSessionTriggerCoverage(tc *TriggerCoverage, all []sess, stepsByID map[string][]stp, eventsByID map[string][]sessEvent, interviewSkills, swarmSkills map[string]bool) {
	seenQ, seenSteps := map[string]bool{}, map[string]bool{}
	handBuildSessions := 0
	for _, x := range all {
		ev := sessionCommitSummaryFromEvents(eventsByID[x.id])
		hasWorkVerb := countSessionBuildQuestions(tc, x, stepsByID[x.id], ev.firstCommitMS, seenQ, interviewSkills)
		hasGate := countSessionV9Coverage(tc, x, stepsByID[x.id], seenSteps, swarmSkills)
		if x.parent == "" && ev.hasNonAmbigCommit {
			tc.BuildSessions++
			if ev.hasHandoff {
				tc.DoDClosedSessions++
			}
			if hasGate {
				tc.DeterministicGateSessions++
			}
			if !hasWorkVerb && x.label == "" {
				handBuildSessions++
			}
		}
	}
	tc.QualifyingPhases = tc.WorkerPhases + handBuildSessions
}

func countSessionV9Coverage(tc *TriggerCoverage, x sess, steps []stp, seen map[string]bool, swarmSkills map[string]bool) bool {
	if x.vendor == historical {
		return false
	}
	hasGate := false
	for _, st := range steps {
		if st.kind == "tool" || st.kind == "assistant" || st.kind == "generation" {
			key := x.vendor + "\x00" + x.label + "\x00" + st.ext
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		if st.kind != "tool" {
			continue
		}
		if swarmSkills[st.skill] {
			tc.SwarmSkillLoads++
		}
		for _, v := range st.verbs {
			switch {
			case v == "fyr gate" || strings.HasPrefix(v, "fyr gate "):
				tc.GateVerbs++
				hasGate = true
			case v == "fyr land" || strings.HasPrefix(v, "fyr land "):
				tc.LandVerbs++
				hasGate = true
			case v == "fyr verify" || strings.HasPrefix(v, "fyr verify ") || v == "fyr close" || strings.HasPrefix(v, "fyr close "):
				hasGate = true
			}
		}
	}
	return hasGate
}

func countSessionBuildQuestions(tc *TriggerCoverage, x sess, steps []stp, firstCommitMS int64, seenQ map[string]bool, interviewSkills map[string]bool) bool {
	interviewSkill := false
	workStarted := false
	hasWorkVerb := false
	for _, st := range steps {
		if interviewSkills[st.skill] {
			interviewSkill = true
		}
		for _, v := range st.verbs {
			if v == "fyr work" || strings.HasPrefix(v, "fyr work ") {
				hasWorkVerb = true
				workStarted = true
			}
		}
		if firstCommitMS > 0 && st.ts >= firstCommitMS {
			workStarted = true
		}
		if st.kind != "tool" || (st.tool != "agy:ask_question" && st.tool != "AskUserQuestion") {
			continue
		}
		if st.ext != "" {
			key := x.vendor + "\x00" + st.ext
			if seenQ[key] {
				continue
			}
			seenQ[key] = true
		}
		if x.parent == "" && interviewSkill && !workStarted {
			continue
		}
		tc.BuildTimeQuestions++
	}
	return hasWorkVerb
}

func sessionCommitSummaryFromEvents(events []sessEvent) sessionCommitSummary {
	var out sessionCommitSummary
	for _, ev := range events {
		if ev.kind == "handoff" {
			out.hasHandoff = true
		} else if ev.kind == "commit" && ev.value != "ambiguous" {
			out.hasNonAmbigCommit = true
			if ev.ts > 0 && (out.firstCommitMS == 0 || ev.ts < out.firstCommitMS) {
				out.firstCommitMS = ev.ts
			}
		}
	}
	return out
}
