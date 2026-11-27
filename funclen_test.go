package fyr

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// funcLimit is rules/code-modularity.md item 1's ceiling.
const funcLimit = 50

// longFuncs is the allowlist: key "path:Name" or "path:Recv.Name" (path from
// the module root, slash-separated, Recv without '*'), value the function's
// length when it was listed. PLAN-2026-10-27-funclen emptied it: every
// function is at most funcLimit lines. A row added here is an exception to
// rules/code-modularity.md item 1, which the ratchet then holds.
var longFuncs = map[string]int{}

// TestFunctionLength is the regrowth guard of PLAN-2026-10-27-funclen: no
// non-test function in a .go file git lists, tracked or untracked but not
// ignored, may pass funcLimit unless longFuncs lists it, a listed one may not
// grow, and a row whose function is gone or back under the limit is stale and
// fails, so the list only empties. An untracked file is read too, so a new
// file is guarded before it is staged (D29, superseding D16's clause).
func TestFunctionLength(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if _, err := os.Stat(".git"); err != nil {
		t.Skip("not a git checkout: an unpacked copy has nothing to compare against")
	}
	files := guardedGoFiles(t)
	if len(files) == 0 {
		t.Fatal("git lists no non-test .go files; the test ran in the wrong directory")
	}
	long := map[string]int{}
	for _, f := range files {
		for k, n := range overLimit(funcLengths(t, f)) {
			long[k] = n
		}
	}
	for _, msg := range funcLengthFindings(long, longFuncs) {
		t.Error(msg)
	}
}

// guardedGoFiles lists the non-test .go files the guard reads, tracked or
// untracked but not ignored: no generated file, nothing under skills/, and no
// tracked file deleted on disk.
func guardedGoFiles(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("git", "ls-files", "-z", "-co", "--exclude-standard", "--", "*.go").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	files, err := filterGoFiles(strings.Split(string(out), "\x00"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// filterGoFiles keeps the paths the guard reads. Only a file gone from disk
// is skipped; any other stat error is returned, so an unreadable directory
// cannot hide its functions from the guard.
func filterGoFiles(paths []string) ([]string, error) {
	var files []string
	for _, p := range paths {
		base := path.Base(p)
		if p == "" || strings.HasSuffix(p, "_test.go") || strings.HasPrefix(p, "skills/") ||
			strings.Contains(base, ".pb.") || strings.Contains(base, "_gen.") {
			continue
		}
		if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("%s: %v", p, err)
		}
		files = append(files, p)
	}
	return files, nil
}

// funcLengths parses one file and returns each function's length in physical
// lines, from the func keyword through the closing brace, keyed as longFuncs
// is; a name repeated in the file (only init can be) takes #2, #3 in order.
func funcLengths(t *testing.T, file string) map[string]int {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("%s does not parse: %v", file, err)
	}
	lengths := map[string]int{}
	seen := map[string]int{}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		key := file + ":" + funcKeyName(fd)
		if seen[key]++; seen[key] > 1 {
			key = fmt.Sprintf("%s#%d", key, seen[key])
		}
		lengths[key] = fset.Position(fd.End()).Line - fset.Position(fd.Pos()).Line + 1
	}
	return lengths
}

// funcKeyName is Name, or Recv.Name with the receiver's '*' and type
// parameters stripped (*T[K] becomes T).
func funcKeyName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	typ := fd.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	switch x := typ.(type) {
	case *ast.IndexExpr:
		typ = x.X
	case *ast.IndexListExpr:
		typ = x.X
	}
	if id, ok := typ.(*ast.Ident); ok {
		return id.Name + "." + fd.Name.Name
	}
	return fd.Name.Name
}

// overLimit keeps the lengths over funcLimit.
func overLimit(lengths map[string]int) map[string]int {
	long := map[string]int{}
	for k, n := range lengths {
		if n > funcLimit {
			long[k] = n
		}
	}
	return long
}

// funcLengthFindings compares the functions over the limit with the
// allowlist and returns one message per offender, sorted: too long and not
// listed, grown past its listed length, or listed but no longer over.
func funcLengthFindings(long, allow map[string]int) []string {
	var msgs []string
	for k, n := range long {
		switch want, ok := allow[k]; {
		case !ok:
			msgs = append(msgs, fmt.Sprintf("%s is %d lines, over %d (rules/code-modularity.md item 1): split it", k, n, funcLimit))
		case n > want:
			msgs = append(msgs, fmt.Sprintf("%s grew to %d lines, listed at %d: a listed function may shrink, never grow", k, n, want))
		}
	}
	for k := range allow {
		if _, ok := long[k]; !ok {
			msgs = append(msgs, fmt.Sprintf("%s is listed in longFuncs but no function by that key is over %d lines: stale, delete its row", k, funcLimit))
		}
	}
	sort.Strings(msgs)
	return msgs
}

// TestFunctionLengthKeys pins the key rules the tree does not exercise
// today: a generic receiver is keyed by its bare type name, and a repeated
// init takes #2 (plan D16).
func TestFunctionLengthKeys(t *testing.T) {
	src := "package p\n\ntype T[K any] struct{}\ntype U[K, V any] struct{}\n\n" +
		"func init() {}\nfunc init() {\n}\n" +
		"func (t *T[K]) A() {}\nfunc (u U[K, V]) B() {}\nfunc F() {}\n"
	file := t.TempDir() + "/p.go"
	if err := os.WriteFile(file, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	got := funcLengths(t, file)
	want := map[string]int{"init": 1, "init#2": 2, "T.A": 1, "U.B": 1, "F": 1}
	if len(got) != len(want) {
		t.Errorf("got %d keys %v, want %d", len(got), got, len(want))
	}
	for k, n := range want {
		if got[file+":"+k] != n {
			t.Errorf("%s:%s = %d, want %d (all: %v)", file, k, got[file+":"+k], n, got)
		}
	}
}

// TestFunctionLengthFiles: a tracked file deleted on disk is skipped, but a
// file the guard cannot stat for another reason fails it (D16), and the
// filters drop test, generated and skills/ files.
func TestFunctionLengthFiles(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 directory")
	}
	dir := t.TempDir()
	keep, locked := dir+"/keep.go", dir+"/locked/a.go"
	for _, p := range []string{keep, locked} {
		if err := os.MkdirAll(path.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("package p\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := filterGoFiles([]string{"", keep, dir + "/gone.go", "x_test.go", "a.pb.go", "b_gen.go", "skills/s.go"})
	if err != nil || len(got) != 1 || got[0] != keep {
		t.Errorf("filterGoFiles = %v, %v; want [%s], nil", got, err, keep)
	}
	if err := os.Chmod(path.Dir(locked), 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(path.Dir(locked), 0o700)
	if got, err := filterGoFiles([]string{keep, locked}); err == nil {
		t.Errorf("filterGoFiles over an unreadable directory = %v, nil; want an error naming %s", got, locked)
	}
}

// TestFunctionLengthBites proves the guard fires with its allowlist empty: a
// function of funcLimit+1 lines is over and one of funcLimit is not, and each
// of the three findings is made: too long and not listed, grown, stale.
func TestFunctionLengthBites(t *testing.T) {
	body := func(name string, lines int) string {
		return "func " + name + "() {\n" + strings.Repeat("\t_ = 0\n", lines-2) + "}\n"
	}
	file := t.TempDir() + "/p.go"
	src := "package p\n\n" + body("Fifty", funcLimit) + body("FiftyOne", funcLimit+1)
	if err := os.WriteFile(file, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	long := overLimit(funcLengths(t, file))
	if len(long) != 1 || long[file+":FiftyOne"] != funcLimit+1 {
		t.Fatalf("over the limit: %v, want only FiftyOne at %d", long, funcLimit+1)
	}
	got := funcLengthFindings(map[string]int{"a.go:Long": 60, "b.go:Grew": 55, "c.go:Kept": 52}, map[string]int{"b.go:Grew": 54, "c.go:Kept": 53, "d.go:Gone": 70})
	want := []string{
		"a.go:Long is 60 lines, over 50 (rules/code-modularity.md item 1): split it",
		"b.go:Grew grew to 55 lines, listed at 54: a listed function may shrink, never grow",
		"d.go:Gone is listed in longFuncs but no function by that key is over 50 lines: stale, delete its row",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("findings:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestFunctionLengthReadsUntracked: a new file is guarded before it is
// staged, and an ignored one is not read (D29).
func TestFunctionLengthReadsUntracked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	for name, body := range map[string]string{"new.go": "package p\n", ".gitignore": "ignored.go\n", "ignored.go": "package p\n"} {
		if err := os.WriteFile(dir+"/"+name, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	if got := guardedGoFiles(t); len(got) != 1 || got[0] != "new.go" {
		t.Errorf("guardedGoFiles = %v, want [new.go]", got)
	}
}

const (
	cognitiveLimit = 15
	nestingLimit   = 4
	pkgFileLimit   = 35
)

// cognitiveCeilingTable pins current functions over cognitiveLimit (key=max;
// ceiling ratchet allows shrinking/removal without failing).
const cognitiveCeilingTable = `
	cmd/fyr/eval.go:readRecordSuite=24 cmd/fyr/eval_flags.go:evalUsage=20 cmd/fyr/record.go:dispatchRecord=17 cmd/fyr/record.go:recordGet=20 cmd/fyr/record_write.go:ctx.resolveRecordEditInput=23 cmd/fyr/run_dispatch.go:ctx.writesState=25
	cmd/fyr/run_dispatch.go:isReportBareProject=17 cmd/fyr/verb_auto.go:runAuto=21 cmd/fyr/verb_auto_checks.go:autoInstallFindings=17 cmd/fyr/verb_auto_sim.go:splitAutoSIMGlobals=17 cmd/fyr/verb_dream.go:runDream=17 cmd/fyr/verb_gate.go:peekPipeSyscallConn=20
	cmd/fyr/verb_graph.go:ctx.validateGraphFlags=16 cmd/fyr/verb_metrics.go:ctx.validateReportAndShowFlags=16 cmd/fyr/verb_models.go:printCatalogText=19 cmd/fyr/verb_models.go:runModelsExplain=18 cmd/fyr/verb_search_hook.go:turnBrainCache.loadTurnDataCtx=18 cmd/fyr/verb_search_hook_precision.go:matchesRecordPrecisionWithFTS=20
	cmd/fyr/verb_search_memory.go:expandSpecPathTokens=16 cmd/fyr/verb_search_memory.go:runMemory=16 cmd/fyr/verb_search_skills.go:extractActionableSkillDigest=18 cmd/fyr/verb_search_skills.go:extractSkillDescription=19 internal/auto/apply_verify.go:checkRawDiff=17 internal/auto/apply_verify.go:parseAutoTests=16
	internal/auto/apply_verify.go:verifyTrailForApply=18 internal/auto/checks.go:resumeFindings=21 internal/auto/checks.go:run.machineFindings=16 internal/auto/checks.go:trailFindings=17 internal/auto/diagnostics.go:run.addressedSignalIDs=21 internal/auto/drafts.go:run.processDraftLinesStats=17
	internal/auto/eval_gate.go:readStarterHoldout=19 internal/auto/eval_gate.go:run.runEvalGateRows=20 internal/auto/eval_proposals.go:hasProposalEvidence=27 internal/auto/eval_proposals.go:proposalHeaderTarget=20 internal/auto/eval_proposals.go:scoreProposalImpact=22 internal/auto/gates.go:gatePower=19
	internal/auto/kickoff.go:resolveAutoTaskRoot=19 internal/auto/rubric.go:Denied=16 internal/auto/status.go:installPendingBanner=22 internal/auto/tasks.go:run.conversationsSinceCutoff=21 internal/auto/tasks.go:run.triggerProjectClass=24 internal/brain/brain.go:Edit=18
	internal/brain/brain_session.go:openSession=17 internal/brain/brain_session.go:session.allocate=26 internal/brain/dream.go:ForDream=19 internal/brain/export.go:readRecords=29 internal/brain/file_io.go:WriteScratch=17 internal/brain/file_io.go:openScratchDir=18
	internal/brain/import_classify.go:classify=17 internal/brain/import_docs.go:readDocs=17 internal/brain/import_global.go:readLogLines=29 internal/brain/import_memory.go:readMemoryStore=18 internal/brain/resume.go:resumeRecords=20 internal/brain/resume_memory.go:extractRepoFileRefs=26
	internal/brain/search.go:Search=24 internal/brain/search.go:decorateMemoryHits=20 internal/brain/search_refs.go:Refs=16 internal/brain/search_refs.go:naming=18 internal/brain/sim_state_io.go:applySIMImportAliases=17 internal/brain/sim_state_io.go:filterSIMExportSince=27
	internal/brain/state_io.go:filterStateImport=26 internal/brain/stdin_doc.go:splitLeadingHeaders=20 internal/brain/turn_records.go:LoadTurnDataCtx=17 internal/brain/turn_records.go:fetchFullTurnRecordsCtx=20 internal/check/anchors_sections.go:checker.sectionRefFindings=21 internal/check/check.go:checker.docsRule=26
	internal/check/docs.go:verbAt=16 internal/check/docs.go:verbFindings=19 internal/check/fs.go:checker.noIndexed=23 internal/check/harness_lint.go:checker.contextRulesBudgetRule=19 internal/check/requirements.go:requirementItems=16 internal/check/specs.go:checker.specsRule=16
	internal/check/style.go:scanFences=19 internal/check/style_mermaid.go:countFlowchartNodes=21 internal/check/style_mermaid.go:countSequenceNodes=24 internal/check/style_mermaid.go:countStateNodes=21 internal/check/style_mermaid.go:skipFlowchartEdge=29 internal/check/traces.go:unfenced=16
	internal/dream/agy_visible.go:workAlone=16 internal/dream/collect_fit.go:extractDecisionWindow=17 internal/dream/collect_sim.go:collectSIMSignals=16 internal/dream/collect_sim.go:collectSIMTargetPaths=16 internal/dream/collect_sources.go:addHiddenCouplingHints=17 internal/dream/timer_remove.go:timerUnit.remove=16
	internal/eval/run.go:checkRows=16 internal/eval/suite.go:checkRow=17 internal/gate/close_guard.go:extractKickoffSection=17 internal/gate/close_guard.go:matchOpenPlanPhaseBoxes=19 internal/gate/judge.go:runGateJudge=19 internal/gate/land.go:summarizeLandFailure=22
	internal/gate/lease.go:loadLeasePatterns=16 internal/gate/verify.go:nonHiddenGoFiles=17 internal/harness/frontmatter.go:isQuoted=21 internal/harness/frontmatter.go:lintEntries=16 internal/harness/frontmatter.go:lintEntryScalar=19 internal/harness/inventory.go:checkManagedTree=18
	internal/harness/inventory_load.go:loadDocs=20 internal/isolate/acp.go:ACPClient.dispatchLine=16 internal/isolate/acp_grants.go:splitShellSegments=17 internal/isolate/acp_grants_words.go:firstCommandBin=30 internal/isolate/acp_grants_words.go:scanWordToken=18 internal/isolate/after.go:Box.ExitChecks=26
	internal/isolate/command.go:resolveCommandBin=20 internal/isolate/creds.go:newCreds=28 internal/isolate/creds.go:scanCreds=20 internal/isolate/creds_archive.go:validGoPkgArchive=28 internal/isolate/creds_line.go:enclosedInCodeSpan=16 internal/isolate/creds_line.go:unescapeLayerSpan=21
	internal/isolate/creds_sqlite.go:stripSQLiteChunkPrefix=16 internal/isolate/creds_testlog.go:streamTestLogFile=17 internal/isolate/creds_token.go:recordWindowCredsSkip=18 internal/isolate/env.go:safeHostPATHDirs=16 internal/isolate/gitdir.go:gitDirProblem=22 internal/isolate/guard.go:fingerprintWith=40
	internal/isolate/keychain_work.go:walkWorkDir=16 internal/isolate/landlock_linux.go:isForbiddenCoverDir=17 internal/isolate/models_stage.go:agyInstallBundleDir=17 internal/isolate/models_stage.go:checkStateDirChain=25 internal/isolate/procs_darwin.go:parseLsof=23 internal/isolate/seatbelt.go:writeSeatbeltRules=19
	internal/isolate/worker_drafts.go:worker.copyDrafts=16 internal/isolate/worker_models.go:checkModels=17 internal/isolate/worker_preflight.go:disjoint=19 internal/metrics/agy_conversation.go:writer.agyGensWithOverrides=20 internal/metrics/agy_tools.go:recordRead=20 internal/metrics/agy_tools.go:writer.agyTool=22
	internal/metrics/daily_digest.go:computeSignalTransitions=16 internal/metrics/diagnostics_health.go:priorWindowSessionRuleConfirmed=18 internal/metrics/evals.go:Store.Evals=16 internal/metrics/evals_acc.go:Store.addSessions=18 internal/metrics/evals_acc.go:rowAcc.addSession=31 internal/metrics/events.go:joinRecords=36
	internal/metrics/events_join.go:joinRootWithBrain=18 internal/metrics/project_roots.go:propagateSessionFallbackRoots=18 internal/metrics/project_roots.go:resolveWorktreeRootsFor=24 internal/metrics/report_autonomy.go:candidateGitRoots=22 internal/metrics/report_evals.go:regularNoSymlinkFile=19 internal/metrics/show.go:Store.populateSummaryExtras=16
	internal/metrics/show_add.go:Summary.addFileRead=19 internal/metrics/show_add.go:Summary.addSession=16 internal/metrics/show_add.go:Summary.addTool=20 internal/metrics/show_add.go:Summary.verbs=36 internal/metrics/show_cohorts.go:accumulateCohortSteps=23 internal/metrics/show_cohorts.go:accumulateRoleSteps=24
	internal/metrics/show_columns.go:populateAdherenceVerbs=40 internal/metrics/show_projects.go:buildRootCanonicalizerWithRoots=19 internal/metrics/show_waves.go:Store.applySkillRatingsTelemetry=25 internal/metrics/show_waves.go:Store.collectSuggestedSkills=20 internal/metrics/sim_model_shift.go:fieldModelsCoverage=24 internal/metrics/sim_model_shift.go:simProblemModelCoverage=21
	internal/metrics/trials.go:Trial.validate=22 internal/metrics/trigger_coverage.go:countSessionBuildQuestions=24 internal/metrics/trigger_coverage.go:countSessionV9Coverage=20 internal/metrics/verb.go:verbFrom=27 internal/metrics/verb_heredoc.go:stripHeredocs=16 internal/metrics/verb_tokenize.go:tokenizer.scanOperator=19
	internal/models/bench.go:SelectBenchTargets=18 internal/models/bench.go:unwrapBenchPayload=26 internal/models/catalog_evidence.go:AggregateWorkEvidence=21 internal/models/catalog_store.go:CheckHomeDir=16 internal/models/models.go:probeWorkDir=16 internal/models/profiles.go:applyChangelogText=16
	internal/models/usage_cache.go:pickCrossFamilyWithQuota=18 internal/redact/blocks.go:blockEnd=19 internal/search/co_activity.go:collectSessionCoActivity=17 internal/search/co_activity.go:upsertCoActivityPairs=23 internal/search/co_activity_hints.go:scope.activeLinkedRecords=28
	internal/search/contracts.go:matchSymsByName=20 internal/search/contracts.go:mergeBrainRefADRs=17 internal/search/contracts.go:partitionContractSources=21 internal/search/contracts_parse.go:extractFailureTables=17 internal/search/contracts_parse.go:splitLevel2Sections=17 internal/search/dream_workspace.go:workspaces.judge=16
	internal/search/export.go:fields=24 internal/search/git_indexstat.go:parseIndexStats=25 internal/search/git_warm_sig.go:hashTrackedWarmEntries=16 internal/search/git_warm_sig.go:scanUntrackedDirEntries=23 internal/search/global.go:HasScopeRecords=20 internal/search/graph_export_metrics.go:mergeImportEdgesIntoTables=19
	internal/search/graph_queries.go:symGraph.collectReverseBlast=24 internal/search/graph_tarjan.go:formatSCCPackageCycles=22 internal/search/graph_tarjan.go:tarjanVisit=24 internal/search/hook_bash_expand.go:scanShellExpansions=25 internal/search/hook_bash_guard.go:checkInteractiveSegment=18 internal/search/hook_bash_guard.go:hasGitFlagWithValues=21
	internal/search/hook_bash_guard.go:hasNonInteractiveCommitFlag=20 internal/search/hook_bash_guard.go:hasNonInteractiveSudoFlag=16 internal/search/hook_bash_guard.go:stepSSHShortFlags=17 internal/search/hook_bash_guard.go:unwrapEnvOrExec=18 internal/search/hook_bash_relay.go:checkRelayGitConfigAlias=24 internal/search/hook_bash_relay.go:extractNestedShellCommand=27
	internal/search/hook_bash_split.go:scanBalancedParenAtom=20 internal/search/hook_bash_split.go:stepUnquotedSplitChar=18 internal/search/hook_coordinator.go:parseWorkerFilesHeader=18 internal/search/hook_deadline.go:runHookWithDeadline=18 internal/search/hook_git_guard.go:findGitRootFast=19 internal/search/hook_git_guard.go:hasGitDirOrConfigBacklink=20
	internal/search/hook_git_index.go:walkGoFilesForState=19 internal/search/hook_input.go:applyHookEnvelopeAliases=16 internal/search/hook_pre_invocation.go:checkDirtyTrackedEntry=24 internal/search/hook_pre_invocation.go:prepareHookScopeReady=19 internal/search/hook_pre_invocation.go:scanDirtyTrackedEntries=17 internal/search/hook_pre_invocation.go:scanDirtyUntrackedEntries=22
	internal/search/hook_pre_invocation_fast.go:collectParallelTurnWork=26 internal/search/hook_pretool.go:checkStatelessPreToolSafety=25 internal/search/hook_pretool.go:enforceBashFileReads=21 internal/search/hook_pretool_ask.go:appendAskValueStrings=19 internal/search/hook_pretool_nudge.go:parseGrepCommandArgs=24 internal/search/hook_pretool_nudge.go:stepPipelineSplitChar=18
	internal/search/hook_session_start.go:buildSessionDailyAlert=21 internal/search/hook_session_start.go:extractTodoNow=16 internal/search/hook_stop_stamp.go:evaluateStopStamp=19 internal/search/hook_stop_stamp.go:sessionTouchedDirtyGo=22 internal/search/hook_subagent_args.go:classifyHookModel=18 internal/search/hook_subagent_args.go:suggestSessionModelNudgeForHome=16
	internal/search/hook_worker_capsule.go:lookupWorkerRecords=21 internal/search/hook_workflow_python.go:findTopLevelEq=26 internal/search/hook_workflow_python.go:scanFStringInterpolations=25 internal/search/hook_workflow_python.go:scanPythonSourceRange=28 internal/search/hook_workflow_scan.go:scanSingleQuotedPythonString=20 internal/search/hook_workflow_scan.go:scanTripleQuotedPythonString=20
	internal/search/parse.go:parseRefs=27 internal/search/parse_go.go:collectStmtLocals=30 internal/search/pkg_imports.go:symGraph.summarizePackages=37 internal/search/process_artifact.go:buildHookEnvelopeWithModelTimed=19 internal/search/rank.go:matchesPromptTerms=16 internal/search/rank.go:scope.sessionActiveFiles=17
	internal/search/read_ledger.go:ParseCapsulePivots=20 internal/search/read_ledger_bash.go:parseBashHeadOrTailMulti=20 internal/search/read_ledger_bash.go:stepTrackedBashDir=17 internal/search/read_ledger_bash_chain.go:extractSimpleSubshell=26 internal/search/read_ledger_bash_chain.go:matchBenignStderrRedirect=17 internal/search/read_ledger_bash_chain.go:matchTrailingOrTrue=16
	internal/search/read_ledger_bash_chain.go:parseBashFileReadsInDir=29 internal/search/read_ledger_bash_chain.go:splitBashReadStatements=21 internal/search/read_ledger_bash_chain.go:stepReadSplitControl=23 internal/search/read_ledger_bash_filter.go:isStdinJqFilter=16 internal/search/read_ledger_bash_filter.go:isStdinPatternFilter=23 internal/search/read_ledger_bash_filter.go:parseBashAwkRead=23
	internal/search/read_ledger_bash_pipe.go:extractPythonReadPaths=23 internal/search/read_ledger_bash_pipe.go:parseBashMatchAllGrepRead=18 internal/search/read_ledger_bash_pipe.go:parseStdinLineCount=17 internal/search/read_ledger_bash_pipe.go:stepGrepShortCluster=17 internal/search/read_ledger_bash_sed.go:isSafeReadOnlySedScript=23 internal/search/read_ledger_bash_sed.go:parseSedArgs=24
	internal/search/read_ledger_bash_sed.go:parseSedMultiRanges=23 internal/search/read_ledger_bash_sed.go:skipSedAddr=21 internal/search/read_ledger_bash_sed.go:skipSedRegex=32 internal/search/reconcile.go:store.applyDirtyPaths=16 internal/search/reconcile.go:store.indexBlobs=22 internal/search/reconcile.go:store.writeFiles=19
	internal/search/search.go:validateSearchModes=19 internal/search/slice_compact.go:extractCompactCapsuleMeta=20 internal/search/slice_compact.go:scope.buildCompactGraphFooter=16 internal/search/slice_compact.go:scope.renderCompactHookCapsule=20 internal/search/slice_compact.go:symGraph.collectCompactEdges=25 internal/search/slice_render.go:scope.appendSliceExtras=20
	internal/search/structural_signals.go:collectUndirectedASTFilePairs=19 internal/search/sym_graph_bounded_like.go:collectBoundedLikeTermsWithMin=17 internal/search/sym_graph_bounded_like.go:matchQualSymbolNamesFromDBCtx=16 internal/search/sym_graph_bounded_like.go:scanSymbolNamesCoveringGoCtx=19 internal/search/sym_graph_bounded_seed.go:scanSeedFileCandidatesCtx=21 internal/search/sym_graph_bounded_sql.go:partitionPrioritySymNames=19
	internal/search/sym_graph_resolve.go:symGraph.collectPackageEdgeWeights=25 internal/search/sym_graph_resolve.go:symGraph.lookupInTarget=16 internal/search/sym_graph_resolve.go:symGraph.resolveRefTargets=24 internal/search/sym_graph_resolve.go:symGraph.resolveUnqualifiedTargets=21 internal/search/symbols_code.go:parseGoFallback=18 internal/search/symbols_go_refs.go:extractExprRefs=36
	internal/search/symbols_ts.go:scanTSLineRefs=25 internal/search/vet.go:CheckADRConflict=16 internal/search/vet.go:extractConflictPhrases=17 internal/search/vet_capsule.go:stepMarkdownFence=16 internal/search/vet_capsule.go:validWorkerCapsuleBlock=18 internal/search/vet_decompose.go:matchClauseFiles=16
	internal/search/vet_decompose_pkg.go:countDistinctExplicitPackages=21 internal/search/vet_history.go:SynthesizeImprovedPrompt=16 internal/search/vet_skills.go:scoreSkillDoc=18 internal/search/vet_skills_catalog.go:extractSkillTermsOpts=19 internal/state/import.go:ensureSafeSandboxDir=16 internal/state/import.go:filterMetricsLedgersByRepo=16
	internal/trail/dirty.go:headEntries=16 internal/trail/trail.go:roots=17 internal/watch/inspect_files.go:inspectArtifactsChecked=17 internal/watch/inspect_proc.go:filterProcs=19
`

const nestingCeilingTable = `
	internal/auto/status.go:installPendingBanner=5 internal/check/fs.go:checker.noIndexed=5 internal/check/style_mermaid.go:countFlowchartNodes=5
	internal/isolate/guard.go:fingerprintWith=5 internal/isolate/worker_preflight.go:disjoint=5 internal/metrics/events.go:joinRecords=5
	internal/metrics/show_columns.go:populateAdherenceVerbs=5 internal/metrics/show_waves.go:Store.applySkillRatingsTelemetry=5 internal/models/catalog_evidence.go:AggregateWorkEvidence=5
	internal/search/hook_git_guard.go:hasGitDirOrConfigBacklink=5 internal/search/hook_workflow_python.go:findTopLevelEq=5 internal/search/parse_go.go:collectStmtLocals=5
	internal/search/read_ledger.go:ParseCapsulePivots=5 internal/search/read_ledger_bash_sed.go:parseSedMultiRanges=5 internal/search/slice_compact.go:extractCompactCapsuleMeta=5
	internal/search/vet.go:CheckADRConflict=5
`

var pkgFileCeiling = map[string]int{
	"cmd/fyr": 51, "internal/auto": 39, "internal/brain": 45,
	"internal/isolate": 52, "internal/metrics": 72, "internal/search": 114,
}

func parseCeilingTable(t *testing.T, table string) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, tok := range strings.Fields(table) {
		k, v, ok := strings.Cut(tok, "=")
		n, err := strconv.Atoi(v)
		if !ok || err != nil {
			t.Fatalf("bad ceiling entry %q", tok)
		}
		out[k] = n
	}
	return out
}

// TestPackageSizeAndComplexityRatchet enforces ceiling ratchets on per-function
// cognitive complexity (<= 15), control-flow nesting depth (<= 4), and
// per-package production file count (<= 35). New offenders or growth past the
// recorded ceiling fail; shrinking or removal passes so later cleanup waves
// stay merge-order independent.
func TestPackageSizeAndComplexityRatchet(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if _, err := os.Stat(".git"); err != nil {
		t.Skip("not a git checkout: an unpacked copy has nothing to compare against")
	}
	files := guardedGoFiles(t)
	if len(files) == 0 {
		t.Fatal("git lists no non-test .go files; the test ran in the wrong directory")
	}
	cognit, nesting, pkgFiles := collectComplexityAndPackageSizes(t, files)
	for _, msg := range ceilingRatchetFindings("cognitive complexity", cognitiveLimit, cognit, parseCeilingTable(t, cognitiveCeilingTable)) {
		t.Error(msg)
	}
	for _, msg := range ceilingRatchetFindings("nesting depth", nestingLimit, nesting, parseCeilingTable(t, nestingCeilingTable)) {
		t.Error(msg)
	}
	for _, msg := range ceilingRatchetFindings("package prod file count", pkgFileLimit, pkgFiles, pkgFileCeiling) {
		t.Error(msg)
	}
}

func collectComplexityAndPackageSizes(t *testing.T, files []string) (cognit, nesting, pkgFiles map[string]int) {
	t.Helper()
	cognit, nesting, pkgFiles = map[string]int{}, map[string]int{}, map[string]int{}
	fset := token.NewFileSet()
	for _, file := range files {
		pkgFiles[path.Dir(file)]++
		f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s does not parse: %v", file, err)
		}
		seen := map[string]int{}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			key := file + ":" + funcKeyName(fd)
			if seen[key]++; seen[key] > 1 {
				key = fmt.Sprintf("%s#%d", key, seen[key])
			}
			cognit[key], nesting[key] = funcComplexityAndNesting(fd)
		}
	}
	return cognit, nesting, pkgFiles
}

func ceilingRatchetFindings(metric string, limit int, got, ceiling map[string]int) []string {
	var msgs []string
	for k, n := range got {
		if n <= limit {
			continue
		}
		maxN, ok := ceiling[k]
		switch {
		case !ok:
			msgs = append(msgs, fmt.Sprintf("%s has %s %d, over %d: simplify or split it", k, metric, n, limit))
		case n > maxN:
			msgs = append(msgs, fmt.Sprintf("%s %s grew to %d, ceiling %d: listed offenders may shrink, never grow", k, metric, n, maxN))
		}
	}
	sort.Strings(msgs)
	return msgs
}

// funcComplexityAndNesting computes cognitive complexity (gocognit rules) and
// maximum control-flow nesting depth (flat if/else-if/else chains share depth)
// in one AST pass over fd.Body.
func funcComplexityAndNesting(fd *ast.FuncDecl) (score, maxDepth int) {
	if fd == nil || fd.Body == nil {
		return 0, 0
	}
	recordDepth := func(d int) {
		if d > maxDepth {
			maxDepth = d
		}
	}
	var walk func(n ast.Node, nest int)
	var walkIf func(s *ast.IfStmt, nest int, isElseIf bool)
	walkIf = func(s *ast.IfStmt, nest int, isElseIf bool) {
		if isElseIf {
			score++
		} else {
			score += 1 + nest
		}
		recordDepth(nest + 1)
		walk(s.Init, nest)
		walk(s.Cond, nest)
		walk(s.Body, nest+1)
		if s.Else == nil {
			return
		}
		if nextIf, ok := s.Else.(*ast.IfStmt); ok {
			walkIf(nextIf, nest, true)
			return
		}
		score++
		walk(s.Else, nest+1)
	}
	walk = func(n ast.Node, nest int) {
		if n == nil {
			return
		}
		switch x := n.(type) {
		case *ast.IfStmt:
			walkIf(x, nest, false)
		case *ast.ForStmt:
			score += 1 + nest
			recordDepth(nest + 1)
			walk(x.Init, nest)
			walk(x.Cond, nest)
			walk(x.Post, nest)
			walk(x.Body, nest+1)
		case *ast.RangeStmt:
			score += 1 + nest
			recordDepth(nest + 1)
			walk(x.X, nest)
			walk(x.Body, nest+1)
		case *ast.SwitchStmt:
			score += 1 + nest
			recordDepth(nest + 1)
			walk(x.Init, nest)
			walk(x.Tag, nest)
			walk(x.Body, nest+1)
		case *ast.TypeSwitchStmt:
			score += 1 + nest
			recordDepth(nest + 1)
			walk(x.Init, nest)
			walk(x.Assign, nest)
			walk(x.Body, nest+1)
		case *ast.SelectStmt:
			score += 1 + nest
			recordDepth(nest + 1)
			walk(x.Body, nest+1)
		case *ast.FuncLit:
			recordDepth(nest + 1)
			walk(x.Body, nest+1)
		case *ast.BranchStmt:
			if x.Label != nil {
				score++
			}
		case *ast.BinaryExpr:
			if x.Op == token.LAND || x.Op == token.LOR {
				score += boolSeqComplexity(x)
				walkNonBoolChildren(x, func(child ast.Node) { walk(child, nest) })
				return
			}
			walk(x.X, nest)
			walk(x.Y, nest)
		default:
			walkDirectChildren(n, func(child ast.Node) { walk(child, nest) })
		}
	}
	walk(fd.Body, 0)
	return score, maxDepth
}

func unwrapParens(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

func boolSeqComplexity(root *ast.BinaryExpr) int {
	var ops []token.Token
	var collect func(e ast.Expr)
	collect = func(e ast.Expr) {
		b, ok := unwrapParens(e).(*ast.BinaryExpr)
		if !ok || (b.Op != token.LAND && b.Op != token.LOR) {
			return
		}
		collect(b.X)
		ops = append(ops, b.Op)
		collect(b.Y)
	}
	collect(root)
	if len(ops) == 0 {
		return 0
	}
	n := 1
	for i := 1; i < len(ops); i++ {
		if ops[i] != ops[i-1] {
			n++
		}
	}
	return n
}

func walkNonBoolChildren(e ast.Expr, fn func(ast.Node)) {
	e = unwrapParens(e)
	if b, ok := e.(*ast.BinaryExpr); ok && (b.Op == token.LAND || b.Op == token.LOR) {
		walkNonBoolChildren(b.X, fn)
		walkNonBoolChildren(b.Y, fn)
		return
	}
	fn(e)
}

func walkDirectChildren(n ast.Node, fn func(ast.Node)) {
	first := true
	ast.Inspect(n, func(child ast.Node) bool {
		if first {
			first = false
			return true
		}
		if child == nil {
			return false
		}
		fn(child)
		return false
	})
}

func TestPackageSizeAndComplexityRatchetBites(t *testing.T) {
	src := `package p
func FlatElseIf(x int) int {
	if x == 1 { return 1 } else if x == 2 { return 2 } else if x == 3 { return 3 } else if x == 4 { return 4 } else if x == 5 { return 5 } else { return 0 }
}
func DeepNest(a bool) {
	if a {
		for i := 0; i < 1; i++ {
			switch i {
			case 0:
				if a {
					if a {
						_ = i
					}
				}
			}
		}
	}
}
func ComplexBool(a, b, c, d bool) bool {
	return a && b || c && d
}
`
	file := t.TempDir() + "/sample.go"
	if err := os.WriteFile(file, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	cognit, nesting, _ := collectComplexityAndPackageSizes(t, []string{file})
	if got := nesting[file+":FlatElseIf"]; got != 1 {
		t.Errorf("FlatElseIf nesting = %d, want 1", got)
	}
	if got := nesting[file+":DeepNest"]; got != 5 {
		t.Errorf("DeepNest nesting = %d, want 5", got)
	}
	if got := cognit[file+":DeepNest"]; got != 15 {
		t.Errorf("DeepNest cognitive = %d, want 15", got)
	}
	if got := cognit[file+":ComplexBool"]; got != 3 {
		t.Errorf("ComplexBool cognitive = %d, want 3", got)
	}
	// Ceiling ratchet: new offender and grown offender fail; shrunk/disappeared allowlisted entries pass.
	bad := ceilingRatchetFindings(
		"cognitive complexity", cognitiveLimit,
		map[string]int{"a.go:New": 16, "b.go:Grew": 21, "c.go:Shrunk": 18, "d.go:Ok": 15},
		map[string]int{"b.go:Grew": 20, "c.go:Shrunk": 22, "e.go:Gone": 30},
	)
	if len(bad) != 2 || !strings.Contains(bad[0], "a.go:New") || !strings.Contains(bad[1], "b.go:Grew") {
		t.Fatalf("ceilingRatchetFindings = %v, want only a.go:New and b.go:Grew", bad)
	}
	pkgBad := ceilingRatchetFindings(
		"package prod file count", pkgFileLimit,
		map[string]int{"internal/newbig": 36, "internal/metrics": 73, "internal/auto": 30},
		map[string]int{"internal/metrics": 72, "internal/auto": 39, "internal/deleted": 40},
	)
	if len(pkgBad) != 2 || !strings.Contains(pkgBad[0], "internal/metrics") || !strings.Contains(pkgBad[1], "internal/newbig") {
		t.Fatalf("pkgFile ceilingRatchetFindings = %v, want internal/metrics and internal/newbig", pkgBad)
	}
}
