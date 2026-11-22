package state

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/metrics"
)

func mustRecordSIM(t *testing.T, home string, in brain.SIMRecordInput) brain.SIMRecordResult {
	t.Helper()
	res, err := brain.RecordSIMAttempt(home, in)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func readImportedTrials(t *testing.T, home string) []metrics.SanitizedTrial {
	t.Helper()
	ms, err := metrics.Open(metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	defer ms.Close()
	mexp, err := metrics.ExportSanitizedState(ms, "", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return mexp.Trials
}

func seedSIMSource(t *testing.T) (string, time.Time) {
	t.Helper()
	home := t.TempDir()
	t1 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	base, obs := 0.42, 0.11
	r1 := mustRecordSIM(t, home, brain.SIMRecordInput{
		RootCause: "Bash read bypass in review skill", MechanismClass: "prompt_guidance",
		SignalID: "sig:global:bash_read_bypass", SourceKind: "auto_harness_edit",
		ProposalID: "PROP-001", CommitSHA: "abcdef1234567", HarnessVersion: "v9.9.0",
		ProposerModel: "gemini-4-neon-xhigh", PolicyModel: "gemini-4-neon-xhigh",
		Files: []string{"skills/fyr-review/SKILL.md"}, What: "Clarify read_file rule",
		PredictedRows: []string{"bash-read"}, EvalVerdict: "kept", FixedRows: []string{"bash-read"},
		EvalPassed: 2, EvalTotal: 2, FieldStatus: "pending_data", FieldMetric: "bash_read_bypass",
		FieldBaseline: &base, Provenance: "derived", Now: t1,
	})
	if _, err := brain.UpdateSIMAttemptFieldOutcome(home, brain.SIMFieldUpdate{
		AttemptID: r1.AttemptID, FieldStatus: "recovered", FieldMetric: "bash_read_bypass", FieldObserved: &obs,
		FieldModels: []brain.SIMModelStatus{{Model: "gemini-4-neon-xhigh", Status: "recovered", Sessions: 5}}, Now: t2,
	}); err != nil {
		t.Fatal(err)
	}
	r2 := mustRecordSIM(t, home, brain.SIMRecordInput{
		RootCause: "Duplicate bash read rule wording", MechanismClass: "prompt_guidance",
		SignalID: "sig:global:bash_read_bypass", SourceKind: "auto_distill",
		ProposalID: "PROP-002", CommitSHA: "1234567abcdef", What: "Alternative wording",
		EvalVerdict: "not_kept", FieldStatus: "n/a", Provenance: "derived", Now: t1.Add(10 * time.Minute),
	})
	if _, err := brain.MergeSIMProblem(home, r2.ProblemID, r1.ProblemID, "derived", t2); err != nil {
		t.Fatal(err)
	}
	if err := metrics.RecordTrial(home, metrics.TrialInput{
		Run: "eval/sim-v13/1", Label: "sim-avoid-repeat", Status: "pass",
		StartedAt: t2, Seconds: 4, Model: "gemini-4-neon-xhigh", PolicyModel: "gemini-4-neon-xhigh",
	}); err != nil {
		t.Fatal(err)
	}
	return home, t2
}

func readZipEntry(t *testing.T, zipPath, name string) ([]byte, os.FileMode, int) {
	t.Helper()
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, rerr := io.ReadAll(rc)
		_ = rc.Close()
		if rerr != nil {
			t.Fatal(rerr)
		}
		return data, f.Mode(), len(zr.File)
	}
	return nil, 0, len(zr.File)
}

func TestStateSIMRoundTrip(t *testing.T) {
	t.Run("round_trip_dryrun_idempotency_and_v13_trial", testSIMRoundTripCore)
	t.Run("default_export_omits_sim", testSIMDefaultExportOmitsEntry)
	t.Run("redaction_and_home_scrubbing", testSIMRedactionAndHomeScrub)
	t.Run("operator_provenance_preserved", testSIMOperatorProvenancePreserved)
	t.Run("v4_v12_three_entry_compat", testSIMV4V12BackwardsCompat)
	t.Run("tampered_and_invalid_sim_rejected", testSIMTamperedAndInvalidRejected)
}

func testSIMRoundTripCore(t *testing.T) {
	srcHome, tNow := seedSIMSource(t)
	bundle := filepath.Join(t.TempDir(), "sim-v5.zip")
	expRes, err := Export(ExportOptions{Home: srcHome, IncludeMemory: true, OutPath: bundle, FyrVersion: "v9.10.0-test", Now: func() time.Time { return tNow }})
	if err != nil {
		t.Fatal(err)
	}
	m := expRes.Manifest
	if m.BrainSchema != 5 || m.MetricsSchema != 13 || !m.IncludeSIM || !m.IncludeMemory || m.Counts.SIMProblems != 2 || m.Counts.SIMAttempts != 2 || m.Counts.SIMAliases != 1 || len(m.SHA256[SIMLedgerEntry]) != 64 {
		t.Fatalf("unexpected manifest = %+v", m)
	}
	if _, mode, count := readZipEntry(t, bundle, SIMLedgerEntry); count != 4 || mode.Perm() != 0o600 {
		t.Fatalf("zip count=%d sim mode=%o, want 4 and 0600", count, mode.Perm())
	}
	assertSIMImportLifecycle(t, bundle)
}

func assertSIMImportLifecycle(t *testing.T, bundle string) {
	t.Helper()
	dstHome := t.TempDir()
	dryRes, err := Import(ImportOptions{Home: dstHome, BundlePath: bundle, IncludeMemory: true, DryRun: true})
	if err != nil || !dryRes.DryRun || dryRes.SIM.Inserted != 5 || dryRes.Insert < 5 {
		t.Fatalf("dry-run import = %+v, err=%v", dryRes, err)
	}
	if entries, err := os.ReadDir(dstHome); err != nil || len(entries) != 0 {
		t.Fatalf("dry-run touched dstHome: %v (%v)", entries, err)
	}
	noMemRes, err := Import(ImportOptions{Home: dstHome, BundlePath: bundle, IncludeMemory: false})
	if err != nil || noMemRes.SIM.Inserted != 0 || noMemRes.SIM.Total != 0 {
		t.Fatalf("default import leaked SIM: %+v, err=%v", noMemRes, err)
	}
	if probs, err := brain.ListSIMProblems(dstHome, brain.SIMListFilter{Status: "all"}); err != nil || len(probs) != 0 {
		t.Fatalf("SIM problems leaked without IncludeMemory: %+v (%v)", probs, err)
	}
	liveRes, err := Import(ImportOptions{Home: dstHome, BundlePath: bundle, IncludeMemory: true})
	if err != nil || liveRes.SIM.Inserted != 5 || liveRes.Insert != 5 {
		t.Fatalf("live SIM import = %+v, err=%v", liveRes, err)
	}
	assertSIMAndV13ImportedState(t, dstHome)
	idemRes, err := Import(ImportOptions{Home: dstHome, BundlePath: bundle, IncludeMemory: true})
	if err != nil || idemRes.Insert != 0 || idemRes.Updated != 0 || idemRes.SIM.Skipped != 5 || idemRes.SkipDuplicate < 5 {
		t.Fatalf("re-import not idempotent: %+v, err=%v", idemRes, err)
	}
}

func assertSIMAndV13ImportedState(t *testing.T, dstHome string) {
	t.Helper()
	det, err := brain.GetSIMProblem(dstHome, "SIM-P002")
	if err != nil || det.Problem.ID != "SIM-P001" || len(det.Aliases) != 1 || det.Aliases[0] != "SIM-P002" || len(det.Attempts) != 2 {
		t.Fatalf("alias resolution after import = %+v, err=%v", det, err)
	}
	var prop1 *brain.SIMAttempt
	for i := range det.Attempts {
		if det.Attempts[i].ProposalID == "PROP-001" {
			prop1 = &det.Attempts[i]
		}
	}
	if prop1 == nil || len(prop1.FieldModels) != 1 || prop1.FieldModels[0].Model != "gemini-4-neon-xhigh" || prop1.FieldModels[0].Sessions != 5 || prop1.FieldBaseline == nil || *prop1.FieldBaseline != 0.42 || prop1.FieldObserved == nil || *prop1.FieldObserved != 0.11 {
		t.Fatalf("attempt fields lost: %+v", det.Attempts)
	}
	if trials := readImportedTrials(t, dstHome); len(trials) != 1 || trials[0].PolicyModel != "gemini-4-neon-xhigh" {
		t.Fatalf("metrics v13 policy_model round-trip = %+v", trials)
	}
}

func testSIMDefaultExportOmitsEntry(t *testing.T) {
	srcHome, tNow := seedSIMSource(t)
	bundle := filepath.Join(t.TempDir(), "default-no-sim.zip")
	expRes, err := Export(ExportOptions{Home: srcHome, OutPath: bundle, Now: func() time.Time { return tNow }})
	if err != nil {
		t.Fatal(err)
	}
	m := expRes.Manifest
	if m.IncludeSIM || m.Counts.SIMProblems != 0 || m.Counts.SIMAttempts != 0 || m.Counts.SIMAliases != 0 || m.SHA256[SIMLedgerEntry] != "" {
		t.Fatalf("default export populated SIM manifest fields: %+v", m)
	}
	if data, _, count := readZipEntry(t, bundle, SIMLedgerEntry); count != 3 || data != nil {
		t.Fatalf("default export zip count=%d sim=%v, want 3 and nil", count, data)
	}
}

func testSIMRedactionAndHomeScrub(t *testing.T) {
	srcHome := t.TempDir()
	tNow := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	tok := "gh" + "p_" + strings.Repeat("x", 36)
	mustRecordSIM(t, srcHome, brain.SIMRecordInput{
		RootCause: "Secret leak " + tok + " under /Users/alice/work/fyr", SourceKind: "operator_commit",
		ProjectScope: "/home/bob/projects/demo", Files: []string{"/Users/alice/work/fyr/skills/s/SKILL.md"},
		What: "Fix /Users/alice/work/fyr token " + tok, EvalVerdict: "none", FieldStatus: "n/a", Provenance: "derived", Now: tNow,
	})
	mustRecordSIM(t, srcHome, brain.SIMRecordInput{
		RootCause: "Host path in /Users/alice/work/clean", SourceKind: "operator_commit",
		ProjectScope: "/home/bob/projects/clean", Files: []string{"/Users/alice/work/clean/a.go"},
		What: "Trim /home/bob/projects/clean path", EvalVerdict: "none", FieldStatus: "n/a", Provenance: "derived", Now: tNow.Add(time.Minute),
	})
	bundle := filepath.Join(t.TempDir(), "scrubbed-sim.zip")
	if _, err := Export(ExportOptions{Home: srcHome, IncludeMemory: true, OutPath: bundle, Now: func() time.Time { return tNow }}); err != nil {
		t.Fatal(err)
	}
	raw, _, _ := readZipEntry(t, bundle, SIMLedgerEntry)
	s := string(raw)
	for _, forbidden := range []string{tok, "ghp ", "/Users/alice", "/home/bob", "users alice", "home bob"} {
		if strings.Contains(s, forbidden) {
			t.Fatalf("sim_ledger.ndjson leaked %q:\n%s", forbidden, s)
		}
	}
	for _, want := range []string{"[redacted by fyr]", "~/work/clean", "~/projects/clean"} {
		if !strings.Contains(s, want) {
			t.Fatalf("sim_ledger.ndjson missing %q:\n%s", want, s)
		}
	}
	if res, err := Import(ImportOptions{Home: t.TempDir(), BundlePath: bundle, IncludeMemory: true}); err != nil || res.SIM.Inserted != 4 {
		t.Fatalf("scrubbed SIM bundle failed re-import: %+v, err=%v", res, err)
	}
}

func testSIMOperatorProvenancePreserved(t *testing.T) {
	srcHome, dstHome := t.TempDir(), t.TempDir()
	tOld := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	tNew := tOld.Add(2 * time.Hour)
	mustRecordSIM(t, dstHome, brain.SIMRecordInput{
		RootCause: "Sacred local operator root cause", SourceKind: "retro_harness",
		What: "Local operator note", EvalVerdict: "operator_verified", FieldStatus: "n/a", Provenance: "operator", Now: tOld,
	})
	mustRecordSIM(t, srcHome, brain.SIMRecordInput{
		RootCause: "Remote derived hijack root cause", SourceKind: "auto_harness_edit", ProposalID: "PROP-REMOTE",
		What: "Remote attempt on P001", EvalVerdict: "not_kept", FieldStatus: "n/a", Provenance: "derived", Now: tNew,
	})
	bundle := filepath.Join(t.TempDir(), "op-guard.zip")
	if _, err := Export(ExportOptions{Home: srcHome, IncludeMemory: true, OutPath: bundle, Now: func() time.Time { return tNew }}); err != nil {
		t.Fatal(err)
	}
	res, err := Import(ImportOptions{Home: dstHome, BundlePath: bundle, IncludeMemory: true})
	if err != nil || res.OperatorPreserved != 1 || res.Conflict != 1 || res.SIM.OperatorPreserved != 1 || res.SIM.Conflicts != 1 || res.SIM.Inserted != 1 {
		t.Fatalf("operator guard stats = %+v, err=%v", res, err)
	}
	det, err := brain.GetSIMProblem(dstHome, "SIM-P001")
	if err != nil || det.Problem.Provenance != "operator" || det.Problem.RootCause != "Sacred local operator root cause" || len(det.Attempts) != 2 {
		t.Fatalf("operator problem altered or attempt missing: %+v, err=%v", det, err)
	}
}

func testSIMV4V12BackwardsCompat(t *testing.T) {
	recLine, _ := json.Marshal(brain.Record{
		RepoID: "git:github.com/org/legacy", ID: "ADR-009", Kind: "adr",
		Status: "accepted", Date: "2026-11-19", Title: "Legacy v4 ADR", Body: "Legacy body", Updated: "2026-11-19T12:00:00Z",
	})
	trialLine := []byte(`{"table":"trials","row":{"run":"legacy/1","label":"eval=row1","harness":"v9.8.0","status":"pass","cause":"","started_at":1763553600000,"seconds":3}}` + "\n")
	brainData := append(recLine, '\n')
	bundle := writeCustomZip(t, []rawZipSpec{
		{name: ManifestEntry, body: validManifestBytes(t, 4, 12, brainData, trialLine)},
		{name: BrainRecordsEntry, body: brainData},
		{name: MetricsSanitizedEntry, body: trialLine},
	})
	dstHome := t.TempDir()
	res, err := Import(ImportOptions{Home: dstHome, BundlePath: bundle, IncludeMemory: true})
	if err != nil || res.Brain.Inserted != 1 || res.Metrics.Trials != 1 || res.SIM.Total != 0 {
		t.Fatalf("v4/v12 3-entry import = %+v, err=%v", res, err)
	}
	if trials := readImportedTrials(t, dstHome); len(trials) != 1 || trials[0].PolicyModel != "" {
		t.Fatalf("legacy trial after import = %+v", trials)
	}
}

type simRejectCase struct {
	name, want string
	incMem     bool
	specs      []rawZipSpec
}

func manifestWithSIMBytes(t *testing.T, simData []byte, includeSIM, includeSHA bool, overrideSHA string) []byte {
	t.Helper()
	sumHex := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	digests := map[string]string{BrainRecordsEntry: sumHex(nil), MetricsSanitizedEntry: sumHex(nil)}
	if includeSHA {
		digests[SIMLedgerEntry] = sumHex(simData)
		if overrideSHA != "" {
			digests[SIMLedgerEntry] = overrideSHA
		}
	}
	raw, err := json.Marshal(Manifest{
		FyrVersion: "v9.10.0", BrainSchema: 5, MetricsSchema: 13, ExportedAt: "2026-11-22T10:00:00Z",
		IncludeMemory: includeSIM, IncludeSIM: includeSIM, RecordClasses: []string{}, RepoIDs: []string{}, SHA256: digests,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func simRejectCases(t *testing.T) []simRejectCase {
	t.Helper()
	good := []byte(`{"table":"sim_aliases","row":{"old_id":"SIM-P002","target_id":"SIM-P001","merged_at":"2026-11-22T10:00:00Z"}}` + "\n")
	badJSON, nullRow := []byte("{bad-json}\n"), []byte(`{"table":"sim_problems","row":null}`+"\n")
	badTbl := []byte(`{"table":"sim_unknown","row":{}}` + "\n")
	badID := []byte(`{"table":"sim_problems","row":{"id":"BAD-ID","root_cause":"x","status":"active","provenance":"derived","opened_at":"2026-11-22T10:00:00Z","updated_at":"2026-11-22T10:00:00Z"}}` + "\n")
	wrap := func(man, sim []byte, withSIM bool, mode fs.FileMode) []rawZipSpec {
		s := []rawZipSpec{{name: ManifestEntry, body: man}, {name: BrainRecordsEntry}, {name: MetricsSanitizedEntry}}
		if withSIM {
			s = append(s, rawZipSpec{name: SIMLedgerEntry, body: sim, mode: mode})
		}
		return s
	}
	return []simRejectCase{
		{"tampered sha256 IncludeMemory=true", "sha256 mismatch", true, wrap(manifestWithSIMBytes(t, good, true, true, strings.Repeat("0", 64)), good, true, 0)},
		{"tampered sha256 IncludeMemory=false", "sha256 mismatch", false, wrap(manifestWithSIMBytes(t, good, true, true, strings.Repeat("0", 64)), good, true, 0)},
		{"sim present without manifest sha256", "sha256 mismatch", false, wrap(manifestWithSIMBytes(t, good, false, false, ""), good, true, 0)},
		{"manifest sha256 missing sim entry", "missing", false, wrap(manifestWithSIMBytes(t, good, true, true, ""), nil, false, 0)},
		{"include_sim true missing sim entry", "missing", false, wrap(manifestWithSIMBytes(t, nil, true, false, ""), nil, false, 0)},
		{"symlink sim entry", "symlink", false, wrap(manifestWithSIMBytes(t, good, true, true, ""), good, true, fs.ModeSymlink|0o777)},
		{"malformed sim NDJSON IncludeMemory=false", "sim_ledger.ndjson:1", false, wrap(manifestWithSIMBytes(t, badJSON, true, true, ""), badJSON, true, 0)},
		{"null sim row", "missing row", false, wrap(manifestWithSIMBytes(t, nullRow, true, true, ""), nullRow, true, 0)},
		{"unknown sim table", "unknown sim table", true, wrap(manifestWithSIMBytes(t, badTbl, true, true, ""), badTbl, true, 0)},
		{"invalid semantic sim problem id", "invalid problem id", true, wrap(manifestWithSIMBytes(t, badID, true, true, ""), badID, true, 0)},
	}
}

func testSIMTamperedAndInvalidRejected(t *testing.T) {
	for _, tc := range simRejectCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			bundle := writeCustomZip(t, tc.specs)
			emptyHome := t.TempDir()
			_, err := Import(ImportOptions{Home: emptyHome, BundlePath: bundle, IncludeMemory: tc.incMem})
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) {
				t.Fatalf("Import err = %v, want %q", err, tc.want)
			}
			if entries, rerr := os.ReadDir(emptyHome); rerr != nil || len(entries) != 0 {
				t.Fatalf("touched target home: %v (%v)", entries, rerr)
			}
		})
	}
}
