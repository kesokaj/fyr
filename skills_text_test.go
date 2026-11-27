package fyr_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/redact"
)

func readSkill(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// section returns the text from the heading line that starts with head up to
// the next heading of the same or a higher level.
func section(t *testing.T, text, head string) string {
	t.Helper()
	i := strings.Index(text, "\n"+head)
	if i < 0 {
		t.Fatalf("no heading %q", head)
	}
	rest := text[i+1:]
	level := len(head) - len(strings.TrimLeft(head, "#"))
	next := regexp.MustCompile(`(?m)^#{1,` + string(rune('0'+level)) + `} `)
	if loc := next.FindStringIndex(rest[len(head):]); loc != nil {
		return rest[:len(head)+loc[0]]
	}
	return rest
}

// TestPushStepsRunTheTrailerCheck: BUG-177's rule graduated into a script
// (plan 2026-10-25-harness-audit-fix D13); both skills that push run it
// before the push, by its installed path.
func TestPushStepsRunTheTrailerCheck(t *testing.T) {
	for path, head := range map[string]string{
		"skills/fyr-handoff/SKILL.md":   "## Step 6",
		"skills/fyr-preflight/SKILL.md": "## Step 6",
	} {
		step := section(t, readSkill(t, path), head)
		if !strings.Contains(step, "scripts/check-trailers.sh") || !strings.Contains(step, "~/.gemini/config/skills/fyr-handoff") {
			t.Errorf("%s %s does not run check-trailers.sh by its installed path", path, head)
		}
		run, push := strings.Index(step, "check-trailers.sh"), strings.LastIndex(step, "push")
		if run < 0 || push < run {
			t.Errorf("%s %s: the trailer check must come before the push", path, head)
		}
	}
}

// TestWorkflowSkillsCarryNoStaleNames pins structural format contracts (ADR-062
// agent paths, one Session form, folded feedback-log retirement under ADR-537 D6,
// and research/spec/requirements ownership boundaries).
func TestWorkflowSkillsCarryNoStaleNames(t *testing.T) {
	if _, err := os.Stat("skills/fyr-feedback-log/SKILL.md"); !os.IsNotExist(err) {
		t.Errorf("skills/fyr-feedback-log/SKILL.md must be removed after folding into fyr-project-memory (err=%v)", err)
	}
	gone := map[string][]string{
		"skills/fyr-retro/SKILL.md":                    {"`agents/<name>.md`", "`.agents/agents/<name>.md`", "fyr-feedback-log"},
		"skills/fyr-harness-audit/references/areas.md": {"derive.go", "`agents/*.md`", "fyr-feedback-log"},
		"skills/fyr-handoff/SKILL.md":                  {"Resume Protocol", "either vendor", "Session ID", "fyr-feedback-log"},
		"skills/fyr-dream/SKILL.md":                    {"fyr-feedback-log"},
		"skills/fyr-project-memory/SKILL.md":           {"fyr-feedback-log"},
		"context/AGENTS.md":                            {"fyr-feedback-log"},
		"README.md":                                    {"fyr-feedback-log"},
		"features/workflows-and-build-control.md":      {"fyr-feedback-log"},
	}
	for path, words := range gone {
		text := readSkill(t, path)
		for _, w := range words {
			if strings.Contains(text, w) {
				t.Errorf("%s still contains stale reference %q", path, w)
			}
		}
	}
	const form = "<first 8 chars of the conversation id>"
	for _, path := range []string{"skills/fyr-handoff/SKILL.md", "skills/fyr-project-memory/SKILL.md", "skills/fyr-retro/SKILL.md"} {
		if !strings.Contains(readSkill(t, path), form) {
			t.Errorf("%s does not use the one Session form %s", path, form)
		}
	}
	memText := readSkill(t, "skills/fyr-project-memory/SKILL.md")
	for _, want := range []string{
		"When to Log", "When NOT to Log", "Log Format", "Categories", "Signal Types", "Behavior Rules",
		"fyr record add feedback --global", "fyr memory add --type procedural", "rule_ref",
	} {
		if !strings.Contains(memText, want) {
			t.Errorf("skills/fyr-project-memory/SKILL.md missing folded passive logging contract %q", want)
		}
	}
	for _, path := range []string{
		"skills/fyr-research/SKILL.md",
		"skills/fyr-spec/SKILL.md",
		"skills/fyr-requirements/SKILL.md",
	} {
		text := readSkill(t, path)
		if !strings.Contains(text, "**Owns:**") || !strings.Contains(text, "**Does not own:**") {
			t.Errorf("%s missing explicit **Owns:** / **Does not own:** trigger boundary", path)
		}
	}
	if !strings.Contains(readSkill(t, "skills/fyr-retro/SKILL.md"), "<name>/agent.md") {
		t.Error("retro's subagent row does not name <name>/agent.md")
	}
}

// TestFeatureSkillEmptyRepoAndWorkersDRow pins structural Step 1 explorer gate
// and Step 3 machine-parsed workers D-row format in skills/fyr-feature/SKILL.md.
func TestFeatureSkillEmptyRepoAndWorkersDRow(t *testing.T) {
	text := readSkill(t, "skills/fyr-feature/SKILL.md")
	step1 := section(t, text, "## Step 1: Scope")
	if !strings.Contains(step1, "`explorer`") {
		t.Errorf("skills/fyr-feature/SKILL.md ## Step 1: Scope missing `explorer` gate")
	}
	step3 := section(t, text, "## Step 3: Cold-reader check")
	if !strings.Contains(step3, "workers: yes | no") {
		t.Errorf("skills/fyr-feature/SKILL.md ## Step 3: Cold-reader check missing `workers: yes | no` D-row marker")
	}
}

// TestColdReaderBudget pins the cold-reader numeric caps (15 tool calls,
// 10 findings, 80 lines; not 30 tool calls) across fyr-feature and reviewer.
func TestColdReaderBudget(t *testing.T) {
	step3 := section(t, readSkill(t, "skills/fyr-feature/SKILL.md"), "## Step 3: Cold-reader check")
	for _, want := range []string{"15 tool calls", "10 findings", "80 lines"} {
		if !strings.Contains(step3, want) {
			t.Errorf("skills/fyr-feature/SKILL.md ## Step 3: Cold-reader check missing budget %q", want)
		}
	}
	if strings.Contains(step3, "30 tool calls") {
		t.Error("skills/fyr-feature/SKILL.md ## Step 3: Cold-reader check still says 30 tool calls")
	}
	rev := readSkill(t, "agents/reviewer/agent.md")
	for _, want := range []string{"15 tool calls", "10 findings", "80 lines"} {
		if !strings.Contains(rev, want) {
			t.Errorf("agents/reviewer/agent.md missing budget %q", want)
		}
	}
}

// TestResumeAndGitCheckAndSingleCommit pins structural CLI/skill references
// (fyr resume, fyr git-check, /fyr-handoff, fyr metrics ingest) across context,
// fyr-feature, and fyr-handoff.
func TestResumeAndGitCheckAndSingleCommit(t *testing.T) {
	ctx := readSkill(t, "context/AGENTS.md")
	for _, want := range []string{"`fyr resume`", "`fyr git-check`", "`/fyr-handoff`", "`fyr metrics ingest`"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("context/AGENTS.md missing structural CLI/skill reference %q", want)
		}
	}
	feat := readSkill(t, "skills/fyr-feature/SKILL.md")
	for _, want := range []string{"/fyr-handoff", "`fyr metrics ingest`"} {
		if !strings.Contains(feat, want) {
			t.Errorf("skills/fyr-feature/SKILL.md missing structural reference %q", want)
		}
	}
	handoff := section(t, readSkill(t, "skills/fyr-handoff/SKILL.md"), "## Step 6")
	for _, want := range []string{"fyr metrics ingest", "push"} {
		if !strings.Contains(handoff, want) {
			t.Errorf("skills/fyr-handoff/SKILL.md Step 6 missing structural step %q", want)
		}
	}
}

func TestCombinedContextRulesAndRepoAgentsUnder15KB(t *testing.T) {
	const budget = 15000
	var paths []string
	for _, pat := range []string{"context/*.md", "rules/*.md"} {
		m, err := filepath.Glob(pat)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, m...)
	}
	paths = append(paths, "AGENTS.md")
	var total int
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		total += len(b)
	}
	if total == 0 || total > budget {
		t.Fatalf("combined context/*.md + rules/*.md + AGENTS.md = %d B, want 1..%d B", total, budget)
	}
	t.Logf("combined context/*.md + rules/*.md + AGENTS.md = %d B (budget %d B)", total, budget)
}

func parseSkillFrontmatterDescription(t *testing.T, path string) string {
	t.Helper()
	raw := readSkill(t, path)
	if !strings.HasPrefix(raw, "---\n") {
		t.Fatalf("%s: missing opening frontmatter fence", path)
	}
	front, _, ok := strings.Cut(raw[4:], "\n---\n")
	if !ok {
		t.Fatalf("%s: missing closing frontmatter fence", path)
	}
	lines := strings.Split(front, "\n")
	for i := 0; i < len(lines); i++ {
		rest, ok := strings.CutPrefix(lines[i], "description:")
		if !ok {
			continue
		}
		val := strings.TrimSpace(rest)
		if val == "|" || val == "|-" || val == ">" || val == ">-" {
			var cont []string
			for j := i + 1; j < len(lines) && (strings.HasPrefix(lines[j], "  ") || lines[j] == ""); j++ {
				if trimmed := strings.TrimSpace(lines[j]); trimmed != "" {
					cont = append(cont, trimmed)
				}
			}
			return strings.Join(cont, " ")
		}
		if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			if u, err := strconv.Unquote(val); err == nil {
				return u
			}
		}
		if len(val) >= 2 && val[0] == '\'' && val[len(val)-1] == '\'' {
			return strings.ReplaceAll(val[1:len(val)-1], "''", "'")
		}
		return val
	}
	t.Fatalf("%s: missing description in frontmatter", path)
	return ""
}

func TestSkillFrontmatterDescriptionsUnder140Chars(t *testing.T) {
	const maxChars = 140
	skills, err := filepath.Glob("skills/*/SKILL.md")
	if err != nil || len(skills) == 0 {
		t.Fatalf("glob skills/*/SKILL.md: %v (n=%d)", err, len(skills))
	}
	for _, p := range skills {
		desc := parseSkillFrontmatterDescription(t, p)
		if desc == "" {
			t.Errorf("%s: empty description", p)
		}
		if n := utf8.RuneCountInString(desc); n > maxChars || len(desc) > maxChars {
			t.Errorf("%s: description is %d chars (%d bytes), want <= %d: %q", p, n, len(desc), maxChars, desc)
		}
	}
}

func TestNoForbiddenStaticCodenamesOrWarningEmoji(t *testing.T) {
	for _, root := range []string{"context", "rules", "agents", "skills"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			text := string(b)
			for _, bad := range []string{"Claude Opus 5.5", "⚠"} {
				if strings.Contains(text, bad) {
					t.Errorf("%s contains forbidden token %q", path, bad)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	searchGoRe := regexp.MustCompile(`internal/search/[^` + "`" + `\s)]*\.go`)
	for _, path := range []string{"skills/fyr-review/SKILL.md", "skills/fyr-swarm/SKILL.md"} {
		text := readSkill(t, path)
		if m := searchGoRe.FindString(text); m != "" {
			t.Errorf("%s leaks internal/search implementation path %q", path, m)
		}
		if strings.Contains(text, "gpt-oss") {
			t.Errorf("%s contains stale static codename \"gpt-oss\"", path)
		}
	}
}

type skillTextCase struct {
	name, path, sec string
	want, forbid    []string
}

func assertSkillTextCases(t *testing.T, cases []skillTextCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := readSkill(t, tc.path)
			if tc.sec != "" {
				text = section(t, text, tc.sec)
			}
			for _, w := range tc.want {
				if !strings.Contains(text, w) {
					t.Errorf("%s missing %q", tc.path, w)
				}
			}
			for _, bad := range tc.forbid {
				if strings.Contains(text, bad) {
					t.Errorf("%s still contains %q", tc.path, bad)
				}
			}
		})
	}
}

// TestV910SkillTemplatesAndR7Alignment pins structural template guarantees
// (runnable distroless + imagetools inspect in docker, ignore_changes + var.git_sha
// in terraform) and PRD.md R7 optional-change / single-handoff markers.
func TestV910SkillTemplatesAndR7Alignment(t *testing.T) {
	assertSkillTextCases(t, []skillTextCase{
		{
			name:   "docker-distroless-runnable",
			path:   "skills/docker/SKILL.md",
			want:   []string{"gcr.io/distroless/static-debian12:nonroot", "docker buildx imagetools inspect gcr.io/distroless/static-debian12:nonroot"},
			forbid: []string{"@sha256:<digest>"},
		},
		{
			name: "terraform-first-apply-bootstrap",
			path: "skills/terraform/SKILL.md",
			want: []string{"ignore_changes = [template[0].containers[0].image]", "var.git_sha"},
		},
		{
			name: "context-r7-single-handoff",
			path: "context/AGENTS.md",
			want: []string{"`change` optional", "handoff"},
		},
		{
			name:   "fyr-bug-r7-single-handoff",
			path:   "skills/fyr-bug/SKILL.md",
			sec:    "## Step 5",
			want:   []string{"handoff", "optional"},
			forbid: []string{"Store `change` (`fyr record add change"},
		},
		{
			name: "fyr-handoff-r7-single-handoff",
			path: "skills/fyr-handoff/SKILL.md",
			want: []string{"`fyr record add change`", "optional"},
		},
	})
}

var frontmatterStopwords = map[string]bool{
	"about": true, "after": true, "all": true, "also": true, "and": true, "any": true,
	"are": true, "before": true, "between": true, "both": true, "but": true, "can": true,
	"during": true, "each": true, "first": true, "for": true, "from": true, "fyr": true,
	"has": true, "have": true, "how": true, "into": true, "its": true, "just": true,
	"load": true, "may": true, "more": true, "most": true, "new": true, "not": true,
	"off": true, "one": true, "only": true, "other": true, "our": true, "out": true,
	"over": true, "own": true, "per": true, "run": true, "runs": true, "same": true,
	"such": true, "than": true, "that": true, "the": true, "then": true, "this": true,
	"through": true, "too": true, "under": true, "use": true, "using": true, "very": true,
	"via": true, "what": true, "when": true, "where": true, "which": true, "who": true,
	"why": true, "with": true, "without": true, "you": true, "your": true,
}

func skillDescKeywords(desc string) map[string]bool {
	words := strings.FieldsFunc(strings.ToLower(desc), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	out := make(map[string]bool, len(words))
	for _, w := range words {
		if len(w) < 3 || frontmatterStopwords[w] {
			continue
		}
		out[w] = true
	}
	return out
}

func findSkillDescOverlaps(descs map[string]string, minShared int) []string {
	names := make([]string, 0, len(descs))
	kw := make(map[string]map[string]bool, len(descs))
	for name, desc := range descs {
		names = append(names, name)
		kw[name] = skillDescKeywords(desc)
	}
	sort.Strings(names)
	var pairs []string
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			a, b := names[i], names[j]
			var shared []string
			for tok := range kw[a] {
				if kw[b][tok] {
					shared = append(shared, tok)
				}
			}
			if len(shared) >= minShared {
				sort.Strings(shared)
				pairs = append(pairs, a+" <-> "+b+" ("+strings.Join(shared, ", ")+")")
			}
		}
	}
	return pairs
}

// TestV910SkillDietBudget pins Phase 17 (WS6c: F-041 golang diet, F-044
// prompt-tax diet for the six >20 KB skills, R7 single-handoff close in
// fyr-feature, and frontmatter keyword deduplication across all skills).
func TestV910SkillDietBudget(t *testing.T) {
	const (
		maxDietBytes    = 13 * 1024 // 13,312 B per trimmed skill
		maxGolangBytes  = 12 * 1024 // 12,288 B for golang SKILL.md
		baselineSix     = 141567    // 26094+25499+23509+23247+22834+20384
		maxCombinedSix  = 106175    // >= 25% byte reduction from baseline
		maxOverlapPairs = 5         // down from 23 pairs sharing >= 2 keywords
	)
	// Must-fail self-check on the overlap detector itself.
	sample := map[string]string{
		"alpha": "React Tailwind web dashboards",
		"beta":  "Angular UI. Not for React or Tailwind.",
	}
	if got := findSkillDescOverlaps(sample, 2); len(got) != 1 {
		t.Fatalf("overlap detector self-check failed: got %v, want 1 pair", got)
	}

	dietSkills := []string{
		"fyr-feature",
		"frontend-ui-economist",
		"fyr-preflight",
		"fyr-review",
		"frontend-ui-modern",
		"fyr-retro",
	}
	combined := 0
	for _, name := range dietSkills {
		p := filepath.Join("skills", name, "SKILL.md")
		raw := readSkill(t, p)
		combined += len(raw)
		if len(raw) > maxDietBytes {
			t.Errorf("%s is %d B, want <= %d B (13 KB)", p, len(raw), maxDietBytes)
		}
		refs, err := filepath.Glob(filepath.Join("skills", name, "references", "*.md"))
		if err != nil || len(refs) == 0 {
			t.Errorf("skills/%s/references/*.md missing progressive reference files", name)
			continue
		}
		linkedLocal := false
		for _, r := range refs {
			if strings.Contains(raw, "references/"+filepath.Base(r)) {
				linkedLocal = true
				break
			}
		}
		if !linkedLocal {
			t.Errorf("%s must link at least one local references/*.md file", p)
		}
	}
	if combined > maxCombinedSix {
		t.Errorf("combined 6 core SKILL.md bytes = %d B, want <= %d B (>= 25%% reduction from %d B)", combined, maxCombinedSix, baselineSix)
	} else {
		pct := float64(baselineSix-combined) * 100 / float64(baselineSix)
		t.Logf("6 trimmed skills combined: %d B (%.1f%% reduction from %d B)", combined, pct, baselineSix)
	}

	goSkill := readSkill(t, "skills/golang/SKILL.md")
	if len(goSkill) > maxGolangBytes {
		t.Errorf("skills/golang/SKILL.md is %d B, want <= %d B (12 KB)", len(goSkill), maxGolangBytes)
	}
	if !strings.Contains(goSkill, "references/service.md") || !strings.Contains(goSkill, "references/testing.md") {
		t.Errorf("skills/golang/SKILL.md must link to references/service.md and references/testing.md")
	}

	// Verify all 7 core skills open with a concise actionable rule digest right below the H1.
	for _, name := range append(append([]string{}, dietSkills...), "golang") {
		p := filepath.Join("skills", name, "SKILL.md")
		raw := readSkill(t, p)
		_, body, ok := strings.Cut(raw[4:], "\n---\n")
		if !ok {
			t.Fatalf("%s: missing closing frontmatter fence", p)
		}
		lines := strings.Split(strings.TrimSpace(body), "\n")
		if len(lines) < 3 || !strings.HasPrefix(lines[0], "# ") {
			t.Errorf("%s: expected H1 heading at start of body", p)
			continue
		}
		head := strings.TrimSpace(strings.Join(lines[1:min(len(lines), 10)], "\n"))
		if len(head) < 120 || strings.HasPrefix(head, "```") {
			t.Errorf("%s: top-600B rule digest below H1 is missing or starts with a fence (%d B)", p, len(head))
		}
	}

	// PRD.md R7 / D10: single-handoff close in fyr-feature (`change` optional).
	assertSkillTextCases(t, []skillTextCase{{
		name: "fyr-feature-r7-single-handoff",
		path: "skills/fyr-feature/SKILL.md",
		want: []string{"`change` optional", "handoff"},
	}})

	// Frontmatter keyword overlap check across all skills/*/SKILL.md.
	paths, err := filepath.Glob("skills/*/SKILL.md")
	if err != nil || len(paths) == 0 {
		t.Fatalf("glob skills/*/SKILL.md: %v (n=%d)", err, len(paths))
	}
	descs := make(map[string]string, len(paths))
	for _, p := range paths {
		name := filepath.Base(filepath.Dir(p))
		descs[name] = parseSkillFrontmatterDescription(t, p)
	}
	overlaps := findSkillDescOverlaps(descs, 2)
	if len(overlaps) > maxOverlapPairs {
		t.Errorf("found %d skill frontmatter pairs sharing >= 2 keywords (want <= %d):\n  %s",
			len(overlaps), maxOverlapPairs, strings.Join(overlaps, "\n  "))
	} else {
		t.Logf("frontmatter keyword overlap pairs (>= 2 shared): %d (limit <= %d)", len(overlaps), maxOverlapPairs)
	}
}

// TestV910SkillDietNoSecretPatterns pins Round 2 of Phase 17: no line in the
// trimmed core skills or the new Phase 17 progressive references/*.md files may
// trip internal/redact or the PEM private-key header check used by fyr gate.
func TestV910SkillDietNoSecretPatterns(t *testing.T) {
	privKeyRe := regexp.MustCompile("-----BEG" + "IN ([A-Z0-9 ]+ )?PRIV" + "ATE KEY-----")
	hasSecret := func(line string) bool {
		return line != "" && (redact.Redact(line) != line || privKeyRe.MatchString(line))
	}
	// Must-fail self-check on the detector (constructed via concat so the test line itself does not trip diff scanners).
	if !hasSecret("scan for " + "tok" + "en=examplevalue") {
		t.Fatal("secret detector self-check failed to flag literal credential assignment")
	}
	checked := []string{
		"skills/fyr-feature/SKILL.md",
		"skills/fyr-feature/references/plan-template.md",
		"skills/fyr-feature/references/cold-reader-and-gates.md",
		"skills/frontend-ui-economist/SKILL.md",
		"skills/frontend-ui-economist/references/token-blocks.md",
		"skills/frontend-ui-economist/references/shadcn-mapping.md",
		"skills/fyr-preflight/SKILL.md",
		"skills/fyr-preflight/references/verification-and-gates.md",
		"skills/fyr-preflight/references/ship-procedure.md",
		"skills/fyr-review/SKILL.md",
		"skills/fyr-review/references/lens-briefs.md",
		"skills/fyr-review/references/fix-and-record.md",
		"skills/frontend-ui-modern/SKILL.md",
		"skills/frontend-ui-modern/references/theme-quickref.md",
		"skills/fyr-retro/SKILL.md",
		"skills/fyr-retro/references/sources-and-harness.md",
		"skills/fyr-retro/references/record-and-apply.md",
		"skills/fyr-project-memory/SKILL.md",
		"skills/golang/SKILL.md",
		"skills_text_test.go",
	}
	for _, p := range checked {
		for i, line := range strings.Split(readSkill(t, p), "\n") {
			if hasSecret(line) {
				t.Errorf("%s:%d matched secret/redaction pattern: %s", p, i+1, strings.TrimSpace(line))
			}
		}
	}
}
