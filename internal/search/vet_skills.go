package search

import (
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

var intentPrimarySkill = map[IntentMode]string{
	IntentBug:     "fyr-bug",
	IntentFeature: "fyr-feature",
	IntentReview:  "fyr-review",
	IntentRelease: "fyr-preflight",
}

// MatchSkills selects the top 1-2 skills (<= 600 B digest + SKILL.md path)
// and sets the mandatory security subagent flag on auth/crypto/secret/frontmatter surfaces.
func MatchSkills(prompt string, intent IntentMode, files []string, catalog []SkillMeta) ([]SkillMeta, bool) {
	low := strings.ToLower(prompt)
	secReq := touchesSecuritySurface(low, files)
	if intent == IntentApproval || intent == IntentTrivial {
		return nil, false
	}
	byName := mergeSkillCatalog(catalog)
	if intent == IntentCorrection {
		return selectNamedSkills(byName, "operator correction", "fyr-feedback-log", "fyr-project-memory"), secReq
	}
	type scoredSkill struct {
		meta  SkillMeta
		score int
	}
	var scored []scoredSkill
	for _, s := range byName {
		if sc, reason := scoreSkillCandidate(s, low, intent, files); sc > 0 {
			cand := s
			cand.Digest = truncateBytes(cand.Digest, maxSkillDigestBytes)
			cand.Reason = reason
			scored = append(scored, scoredSkill{meta: cand, score: sc})
		}
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].meta.Name < scored[j].meta.Name
	})
	var out []SkillMeta
	for i := 0; i < len(scored) && len(out) < 2; i++ {
		out = append(out, scored[i].meta)
	}
	return out, secReq
}

func selectNamedSkills(byName map[string]SkillMeta, reason string, names ...string) []SkillMeta {
	var out []SkillMeta
	for _, name := range names {
		if s, ok := byName[name]; ok {
			s.Digest = truncateBytes(s.Digest, maxSkillDigestBytes)
			s.Reason = reason
			out = append(out, s)
		}
	}
	return out
}

func scoreSkillCandidate(s SkillMeta, lowPrompt string, intent IntentMode, files []string) (int, string) {
	if isDiagnosticOrNegatedSkillMention(s.Name, lowPrompt) {
		return 0, ""
	}
	score, reason := scoreSkillSurface(s.Name, lowPrompt, intent, files)
	if primary, ok := intentPrimarySkill[intent]; ok && s.Name == primary {
		score, reason = 100, "intent="+string(intent)
	}
	if containsAnyWord(lowPrompt, s.Name) {
		score += 40
		if reason == "" {
			reason = "named in prompt"
		}
	}
	return score, reason
}

func isDiagnosticOrNegatedSkillMention(name, lowPrompt string) bool {
	if !containsAnyWord(lowPrompt, name) {
		return false
	}
	for _, phrase := range []string{
		"loading the " + name + " skill", "loads the " + name + " skill", "loading " + name + " skill",
		"triggering the " + name + " skill", "triggers the " + name + " skill", "over-triggers " + name,
		name + " skill very often", "don't use " + name, "do not use " + name, "never use " + name,
		"without " + name, "stop loading " + name, "instead of " + name,
	} {
		if strings.Contains(lowPrompt, phrase) {
			return true
		}
	}
	if strings.Contains(lowPrompt, "why ") && strings.Contains(lowPrompt, name+" skill") {
		return true
	}
	return false
}

func scoreSkillSurface(name, lowPrompt string, intent IntentMode, files []string) (int, string) {
	if sc, r := scoreLangOrUISkill(name, lowPrompt, files); sc > 0 {
		return sc, r
	}
	if sc, r := scoreInfraOrAPISkill(name, lowPrompt, files); sc > 0 {
		return sc, r
	}
	return scoreFyrWorkflowSkill(name, lowPrompt, intent, files)
}

func scoreLangOrUISkill(name, lowPrompt string, files []string) (int, string) {
	switch name {
	case "golang":
		goCount := countExt(files, ".go")
		if goCount > 0 || containsAnyWord(lowPrompt, "golang", "goroutine", "slog", "httptest", "gofmt", "govulncheck") ||
			containsAnySubstr(lowPrompt, "go test", "go build", "go run", "go vet", "go.mod", "go code", "in go") {
			return 70 + min(15, goCount*3), "Go code surface"
		}
	case "typescript":
		tsCount := countExt(files, ".ts", ".tsx")
		if tsCount > 0 || containsAnyWord(lowPrompt, "typescript", "tsconfig", "vitest") {
			return 70 + min(15, tsCount*3), "TypeScript surface"
		}
	case "bash":
		if isBashSkillSurface(lowPrompt, files) {
			return 70, "shell script surface"
		}
	case "frontend-ui-economist":
		if hasExt(files, ".html", ".css", ".svg") || containsAnySubstr(lowPrompt, "plain html", "editorial ui", "svg chart", "economist") {
			return 72, "editorial HTML/CSS/SVG UI surface"
		}
	case "frontend-ui-modern":
		if hasExt(files, ".tsx") || containsAnyWord(lowPrompt, "shadcn", "tailwind", "react", "tanstack") {
			return 72, "React/Tailwind/shadcn UI surface"
		}
	case "codebase-design":
		if containsAnySubstr(lowPrompt, "deep module", "module boundary", "interface design", "decouple", "seams") {
			return 68, "module/interface design surface"
		}
	}
	return 0, ""
}

func isBashSkillSurface(lowPrompt string, files []string) bool {
	if promptHasExt(lowPrompt, ".sh") {
		return true
	}
	shCount := countExt(files, ".sh")
	if shCount > 0 && shCount*2 > len(files) {
		return true
	}
	return containsAnySubstr(lowPrompt, "shell script", "bash script", "setup wizard", "bash wizard", "interactive wizard") ||
		containsAnyWord(lowPrompt, "zsh", "shfmt", "shellcheck", "jq", "yq", "awk", "sed", "set_gcp_secret")
}

func scoreInfraOrAPISkill(name, lowPrompt string, files []string) (int, string) {
	switch name {
	case "docker":
		if hasBaseOrExt(files, "dockerfile", "docker-compose") || containsAnyWord(lowPrompt, "docker", "dockerfile", "container", "colima") {
			return 72, "container/Dockerfile surface"
		}
	case "cloud-run-deploy":
		if containsAnySubstr(lowPrompt, "cloud run", "gcloud run", "artifact registry") {
			return 74, "Cloud Run deploy surface"
		}
	case "data-gcp":
		if hasExt(files, ".sql") || containsAnySubstr(lowPrompt, "cloud sql", "postgres", "postgresql", "pgx", "golang-migrate") {
			return 74, "database/Cloud SQL surface"
		}
	case "api-auth-gcp":
		if containsAnyWord(lowPrompt, "iap", "oidc", "firebase") || containsAnySubstr(lowPrompt, "identity-aware proxy", "id token") {
			return 74, "GCP API auth surface"
		}
	case "json-api-style":
		if containsAnySubstr(lowPrompt, "json api", "lowercamelcase", "pagetoken", "nextpagetoken", "aip-") {
			return 68, "JSON API contract surface"
		}
	case "mermaid-syntax":
		if containsAnyWord(lowPrompt, "mermaid", "flowchart", "sequencediagram", "statediagram", "erdiagram") {
			return 75, "Mermaid diagram surface"
		}
	case "testing-strategy":
		if containsAnyWord(lowPrompt, "test", "tests", "coverage", "verifier", "e2e") {
			return 60, "verification/testing surface"
		}
	}
	return 0, ""
}

var fyrWorkflowSkillRules = map[string]struct {
	score  int
	reason string
	words  []string
	subs   []string
}{
	"fyr-spec":           {65, "specification surface", []string{"spec", "specify", "contract", "interface"}, nil},
	"spec":               {65, "specification surface", []string{"spec", "specify", "contract", "interface"}, nil},
	"fyr-project-memory": {85, "memory directive", []string{"remember", "memory", "forget"}, nil},
	"project-memory":     {85, "memory directive", []string{"remember", "memory", "forget"}, nil},
	"fyr-work":           {78, "fyr work orchestration surface", []string{"sandbox", "worker", "workers"}, []string{"fyr work", "/fyr-work", "kickoff"}},
	"fyr-handoff":        {85, "session handoff surface", []string{"handoff", "fyr-handoff"}, []string{"hand off", "wrap up for next session"}},
	"fyr-retro":          {85, "session retrospective surface", []string{"retro", "retrospective", "fyr-retro"}, nil},
	"fyr-distill":        {82, "skill distillation surface", []string{"distill", "fyr-distill"}, []string{"into a skill", "reusable skill"}},
	"fyr-dream":          {82, "memory consolidation surface", nil, []string{"fyr dream", "/fyr-dream", "consolidate memory", "dreamer"}},
	"fyr-harness-audit":  {88, "harness audit surface", nil, []string{"harness audit", "fyr-harness-audit", "audit the harness"}},
	"fyr-triage":         {86, "issue/PR and backlog triage surface", []string{"triage", "fyr-triage", "wontfix", "needs-triage", "needs-info", "ready-for-agent"}, []string{"/fyr-triage", "agent brief", "needs my attention"}},
}

func scoreFyrWorkflowSkill(name, lowPrompt string, intent IntentMode, files []string) (int, string) {
	switch name {
	case "fyr-explain-project", "explain-project":
		if intent == IntentQuestion && (strings.Contains(lowPrompt, "explain") || strings.Contains(lowPrompt, "walk me through")) {
			return 95, "intent=QUESTION (onboarding)"
		}
	case "fyr-docs":
		if containsAnyWord(lowPrompt, "fyr-docs", "docs-writer", "bluf", "docset") ||
			containsAnySubstr(lowPrompt, "/fyr-docs", "audit docs", "audit the docs", "audit the documentation", "docs audit", "airy docs") {
			return 84, "documentation standard/audit surface"
		}
		if containsAnyWord(lowPrompt, "documentation", "readme", "onboarding", "runbook") ||
			containsAnySubstr(lowPrompt, "write docs", "update docs", "edit docs", "review docs") ||
			(hasBaseOrExt(files, "readme.md", "sdd.md", "tdd.md") && !containsAnyWord(lowPrompt, "typo", "rename")) {
			return 73, "documentation surface"
		}
	case "fyr-index-project":
		if strings.Contains(lowPrompt, "fyr-index-project") || (containsAnyWord(lowPrompt, "index", "indexing", "search.db") && !containsAnyWord(lowPrompt, "refresh", "rebuild")) {
			return 90, "project indexing surface"
		}
	case "fyr-refresh-project-index":
		if strings.Contains(lowPrompt, "fyr-refresh-project-index") || (containsAnyWord(lowPrompt, "index", "search.db") && containsAnyWord(lowPrompt, "refresh", "rebuild", "stale")) {
			return 92, "project index refresh surface"
		}
	case "fyr-work":
		if (len(fyrWorkflowSkillRules[name].words) > 0 && containsAnyWord(lowPrompt, fyrWorkflowSkillRules[name].words...)) ||
			(len(fyrWorkflowSkillRules[name].subs) > 0 && containsAnySubstr(lowPrompt, fyrWorkflowSkillRules[name].subs...)) ||
			(!isKickoffLow(lowPrompt) && hasBatchOrMultiPhaseCues(lowPrompt, lowPrompt)) {
			return fyrWorkflowSkillRules[name].score, fyrWorkflowSkillRules[name].reason
		}
	default:
		if rule, ok := fyrWorkflowSkillRules[name]; ok {
			if (len(rule.words) > 0 && containsAnyWord(lowPrompt, rule.words...)) ||
				(len(rule.subs) > 0 && containsAnySubstr(lowPrompt, rule.subs...)) {
				return rule.score, rule.reason
			}
		}
	}
	return 0, ""
}

func touchesSecuritySurface(lowPrompt string, files []string) bool {
	if containsAnySubstr(
		lowPrompt,
		"auth", "authn", "authz", "oauth", "oidc", "jwt", "iap",
		"auth token", "api token", "id token", "bearer token", "oauth token",
		"password", "crypto", "cipher", "hmac", "sha256", "tls", "secret",
		"redact", "credential", "iam", "permission", "frontmatter", "allowlist",
		"allow-list", "skillkeys", "agentkeys", "knowntools", "inject", "injection", "sanitize",
	) {
		return true
	}
	for _, f := range files {
		if containsAnySubstr(strings.ToLower(f), "redact", "security", "auth", "guard", "inventory.go", "go.mod") {
			return true
		}
	}
	return false
}

func containsAnySubstr(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func extractPromptAndSliceFiles(prompt string, sliceFiles []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range sliceFiles {
		if f = strings.TrimSpace(f); f != "" && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	for _, m := range fileTokenRe.FindAllString(prompt, -1) {
		if m = strings.TrimSpace(strings.Trim(m, "`'\".,;:()")); m != "" && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

func containsAnyWord(low string, words ...string) bool {
	tokens := strings.FieldsFunc(low, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-'
	})
	for _, t := range tokens {
		if slices.Contains(words, t) {
			return true
		}
	}
	return false
}

func hasExt(files []string, exts ...string) bool {
	return countExt(files, exts...) > 0
}

func countExt(files []string, exts ...string) int {
	n := 0
	for _, f := range files {
		if slices.Contains(exts, strings.ToLower(filepath.Ext(f))) {
			n++
		}
	}
	return n
}

func promptHasExt(lowPrompt string, exts ...string) bool {
	for _, m := range fileTokenRe.FindAllString(lowPrompt, -1) {
		m = strings.Trim(m, "`'\".,;:()")
		if slices.Contains(exts, strings.ToLower(filepath.Ext(m))) {
			return true
		}
	}
	return false
}

func hasBaseOrExt(files []string, subs ...string) bool {
	for _, f := range files {
		low := strings.ToLower(filepath.Base(f))
		for _, sub := range subs {
			if strings.Contains(low, sub) {
				return true
			}
		}
	}
	return false
}

func defaultSkillCatalog() []SkillMeta {
	return []SkillMeta{
		{Name: "fyr-bug", Path: "skills/fyr-bug/SKILL.md", Digest: "Workflow for fixing broken behavior: problem note, goldfish diagnosis on symptom/repro only, failing test before the fix, smallest fix, review, bug record."},
		{Name: "fyr-feature", Path: "skills/fyr-feature/SKILL.md", Digest: "Workflow for feature work: scope confirmation, plan record with phases and task checkboxes, cold-reader check before code, one phase per checkpoint, e2e verification."},
		{Name: "fyr-feedback-log", Path: "skills/fyr-feedback-log/SKILL.md", Digest: "Passive correction logger: when the operator corrects or overrides agent behavior, record a structured global feedback record (`fyr record add feedback --global`)."},
		{Name: "fyr-project-memory", Path: "skills/fyr-project-memory/SKILL.md", Digest: "Save and load project memory through `fyr memory add` and `fyr memory show` when the operator states a preference, correction, or project constraint."},
		{Name: "fyr-review", Path: "skills/fyr-review/SKILL.md", Digest: "Review working tree, git range, PR, or paths through reviewer, security, verifier, and explorer subagents (`--code`, `--security`, `--docs`, `--tests`)."},
		{Name: "fyr-preflight", Path: "skills/fyr-preflight/SKILL.md", Digest: "Release gate and release path (`/fyr-preflight vX.Y.Z`): Definition of Done, reviewer and security dispatch over release diff, SHIP/NO-SHIP report, annotated tag."},
		{Name: "golang", Path: "skills/golang/SKILL.md", Digest: "Go standards: `cmd/<app>` + `internal/`, stdlib first, `log/slog` JSON, table-driven tests with `-race`, hand-written fakes, functions <= 50 lines."},
		{Name: "typescript", Path: "skills/typescript/SKILL.md", Digest: "TypeScript standards: strict tsconfig, narrowing, ESM module boundaries, runtime validation at boundaries, Vitest and Playwright tiers."},
		{Name: "bash", Path: "skills/bash/SKILL.md", Digest: "Bash and shell scripting: `set -euo pipefail`, safe quoting, `jq`/`rg`/`fd` pipelines, shellcheck compliance, and interactive setup wizards (`set_gcp_secret`)."},
		{Name: "testing-strategy", Path: "skills/testing-strategy/SKILL.md", Digest: "Decide test tiers owed by a change: unit with `-race`, integration, API `httptest`, e2e, and `GUIDE-testing` maintenance."},
		{Name: "fyr-docs", Path: "skills/fyr-docs/SKILL.md", Digest: "Airy, visual-first documentation standard and docset audit: BLUF (Bottom Line Up Front), short prose, Mermaid diagrams, and `<details>` folding."},
		{Name: "fyr-triage", Path: "skills/fyr-triage/SKILL.md", Digest: "Triage GitHub issues/PRs and local `fyr` queues (`TODO.md` Backlog, `BUG-*`, `PROPOSAL-*`) via redundancy and `PRD.md`/ADR checks into Agent Briefs or `/fyr-work` Kickoffs."},
		{Name: "fyr-spec", Path: "skills/fyr-spec/SKILL.md", Digest: "Write or update a `SPEC-<app>-<topic>` record concrete enough that two implementers converge and `fyr check` verifies it."},
		{Name: "fyr-explain-project", Path: "skills/fyr-explain-project/SKILL.md", Digest: "Explain a codebase to the operator with `file:line` entry points, package flow, and trail resume order (`GUIDE-onboarding`)."},
		{Name: "fyr-index-project", Path: "skills/fyr-index-project/SKILL.md", Digest: "Enable MVP indexing and AST graphing (`.agents/scratch/search.db`, `fyr index`, `fyr search --map`) in an existing or unscaffolded project with zero root-doc scaffolding."},
		{Name: "fyr-refresh-project-index", Path: "skills/fyr-refresh-project-index/SKILL.md", Digest: "Reconcile (`fyr index`) or clean-rebuild (`fyr index --rebuild`) the project's `.agents/scratch/search.db`, print `fyr search --map`, and report skipped files or noindex directories."},
		{Name: "frontend-ui-economist", Path: "skills/frontend-ui-economist/SKILL.md", Digest: "Editorial, report and airy UI in plain HTML/CSS/SVG: six themes on one token contract, an SVG chart kit, a white-space method."},
		{Name: "frontend-ui-modern", Path: "skills/frontend-ui-modern/SKILL.md", Digest: "Modern web-app UI on shadcn/ui, Tailwind and React: dashboards, tables, forms, theming, dark mode."},
		{Name: "codebase-design", Path: "skills/codebase-design/SKILL.md", Digest: "Shared vocabulary for designing deep modules, finding deepening opportunities, and placing seams for testable, AI-navigable code."},
		{Name: "docker", Path: "skills/docker/SKILL.md", Digest: "Container-based development with Docker and Colima: building images, compose services, Dockerfiles, volumes and networking."},
		{Name: "cloud-run-deploy", Path: "skills/cloud-run-deploy/SKILL.md", Digest: "Google Cloud Run deployment mechanics: build locally, push to Artifact Registry, deploy, verify, roll back."},
		{Name: "data-gcp", Path: "skills/data-gcp/SKILL.md", Digest: "Cloud SQL PostgreSQL for Go on Cloud Run: pgx over /cloudsql socket, golang-migrate job, owner/runtime role split, indexes."},
		{Name: "api-auth-gcp", Path: "skills/api-auth-gcp/SKILL.md", Digest: "Cloud Run caller verification: IAP, Firebase Auth, OIDC identity tokens for service-to-service, Scheduler, Tasks, and Pub/Sub."},
		{Name: "json-api-style", Path: "skills/json-api-style/SKILL.md", Digest: "Google AIP JSON API design: lowerCamelCase keys, RFC 3339 timestamps, string enums, pageSize/pageToken, structured errors."},
		{Name: "mermaid-syntax", Path: "skills/mermaid-syntax/SKILL.md", Digest: "Error-free Mermaid v11 diagrams: flowcharts, sequence, state, class, ER, and XY charts with quoted special-character labels."},
		{Name: "fyr-work", Path: "skills/fyr-work/SKILL.md", Digest: "Lead plan phases or task lists with isolated fyr work sandbox workers and squash-merge each PASS."},
		{Name: "fyr-handoff", Path: "skills/fyr-handoff/SKILL.md", Digest: "Compact the session into an immutable handoff record with a Kickoff block, commit, push, and fyr metrics ingest."},
		{Name: "fyr-retro", Path: "skills/fyr-retro/SKILL.md", Digest: "Session retrospective: analyze corrections, memory promotions, bugs, and metrics to propose minimal rule/skill improvements."},
		{Name: "fyr-distill", Path: "skills/fyr-distill/SKILL.md", Digest: "Detect repeated workflows or domain knowledge in the session and distill them into a reusable skill."},
		{Name: "fyr-dream", Path: "skills/fyr-dream/SKILL.md", Digest: "Interactive project memory consolidation: assign verdicts (keep, graduated, stale, broken, merge, promote) to memory records."},
		{Name: "fyr-harness-audit", Path: "skills/fyr-harness-audit/SKILL.md", Digest: "Audit the harness checkout area by area with read-only agents and write a severity-ranked report and action plan."},
	}
}

func mergeSkillCatalog(custom []SkillMeta) map[string]SkillMeta {
	byName := map[string]SkillMeta{}
	for _, s := range defaultSkillCatalog() {
		byName[s.Name] = s
	}
	for _, s := range custom {
		if s.Name == "" {
			continue
		}
		if s.Digest == "" {
			if def, ok := byName[s.Name]; ok {
				s.Digest = def.Digest
			} else {
				s.Digest = s.Description
			}
		}
		if s.Path == "" {
			s.Path = "skills/" + s.Name + "/SKILL.md"
		}
		byName[s.Name] = s
	}
	return byName
}
