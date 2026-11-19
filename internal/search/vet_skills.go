package search

import (
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
	secReq := intent != IntentQuestion && touchesSecuritySurface(low, files)
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
			cand.Digest = truncateBytesUTF8(cand.Digest, maxSkillDigestBytes)
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
			s.Digest = truncateBytesUTF8(s.Digest, maxSkillDigestBytes)
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
	"fyr-swarm":          {78, "fyr swarm parallel orchestration surface", []string{"swarm", "parallel", "worker", "workers"}, []string{"/fyr-swarm", "fyr gate", "fyr land", "run_workflow"}},
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
	case "fyr-swarm":
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
