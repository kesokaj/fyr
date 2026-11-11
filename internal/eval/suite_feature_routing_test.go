package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func featureRoutingOutcomes() map[string][]outcome {
	conv := func(name, text string) string {
		return "mkdir -p \"$SB/.gemini/antigravity-cli/conversations\"\n" +
			"printf '%s\\n' '" + text + "' >\"$SB/.gemini/antigravity-cli/conversations/" + name + ".db\"\n"
	}
	goodOut := "echo 'Caller: internal/greet/greet.go:22-30 (RenderGreeting). Model pick: fyr models --pick builder. Multi-phase waves: /fyr-work.' >\"$OUT\"\n"
	goodRoute := conv("hook", `[VET_IMPROVED_PROMPT] route: fyr search --slice`)
	goodCmd := conv("cmd", `{"CommandLine":"fyr search --affected BannerFormatter.Format"}`)

	return map[string][]outcome{
		"feature-routing": {
			{
				"cites greet.go range, names fyr models --pick and /fyr-work, logs vet route and fyr search query, clean tree",
				true,
				goodOut + goodRoute + goodCmd,
			},
			{
				"missing greet.go line-range citation in reply",
				false,
				"echo 'Use fyr models --pick builder and /fyr-work.' >\"$OUT\"\n" + goodRoute + goodCmd,
			},
			{
				"missing fyr models --pick in reply",
				false,
				"echo 'Caller: internal/greet/greet.go:22-30 (RenderGreeting). Waves: /fyr-work.' >\"$OUT\"\n" + goodRoute + goodCmd,
			},
			{
				"missing /fyr-work in reply",
				false,
				"echo 'Caller: internal/greet/greet.go:22-30 (RenderGreeting). Model: fyr models --pick builder.' >\"$OUT\"\n" + goodRoute + goodCmd,
			},
			{
				"missing advisory route line in conversation db",
				false,
				goodOut + goodCmd,
			},
			{
				"no fyr search graph or refs command in conversation db",
				false,
				goodOut + goodRoute + conv("cmd", `{"CommandLine":"ls -la"}`),
			},
			{
				"left working tree dirty",
				false,
				goodOut + goodRoute + goodCmd + "echo '// dirty' >>internal/greet/greet.go\n",
			},
		},
	}
}

type featureRoutingRowSpec struct {
	when string
	use  string
	not  string
}

var wantFeatureRoutingRows = []featureRoutingRowSpec{
	{"Code or record lookup", "`fyr search --slice`", "`grep`"},
	{"Session start", "`fyr resume`", "Reading root files one by one"},
	{"New behaviour or design question", "`/fyr-feature`", "Building straight away"},
	{"2+ phases or independent tasks", "`/fyr-work`", "Sequential hand-execution"},
	{"Choosing a model for any role", "`fyr models --pick <role> [--tier pro\\|flash]`", "Hardcoded or guessed model IDs"},
	{"Anything broken or failing", "`/fyr-bug`", "Patching straight away"},
	{"Queue of issues, PRs, or Backlog", "`/fyr-triage`", "Ad-hoc cherry-picking"},
	{"Review or judgement needed", "`reviewer` / `security` / `verifier` (`--judge` cross-family)", "Unchecked self-review"},
	{"Skill, rule, or agent changed", "`fyr eval <row>`", "Untested behavioural claims"},
	{"Docs written or audited", "`fyr-docs` + `fyr check`", "Dense prose walls"},
	{"Phase or session end", "`/fyr-handoff`", "Ending silently"},
	{"Idle consolidation or usage audit", "`fyr report --md`", "Leaving telemetry unread"},
}

func featureRoutingRuleProblems(ruleText string) []string {
	var errs []string
	if !strings.Contains(ruleText, "trigger: always_on") {
		errs = append(errs, "rules/fyr-features.md missing trigger: always_on")
	}
	for _, r := range wantFeatureRoutingRows {
		found := false
		for _, line := range strings.Split(ruleText, "\n") {
			if strings.HasPrefix(line, "|") && strings.Contains(line, r.when) && strings.Contains(line, r.use) && strings.Contains(line, r.not) {
				found = true
				break
			}
		}
		if !found {
			errs = append(errs, "rules/fyr-features.md missing row "+r.when+" -> "+r.use)
		}
	}
	return errs
}

func TestFeatureRoutingRuleAndEvalRow(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	ruleBytes, err := os.ReadFile(filepath.Join(root, "rules", "fyr-features.md"))
	if err != nil {
		t.Fatalf("reading rules/fyr-features.md: %v", err)
	}
	ruleText := string(ruleBytes)
	if errs := featureRoutingRuleProblems(ruleText); len(errs) > 0 {
		t.Fatalf("rules/fyr-features.md failed trigger check:\n%s", strings.Join(errs, "\n"))
	}

	// Must-fail mutations: removing any one of the 12 trigger rows must fail.
	for _, r := range wantFeatureRoutingRows {
		mutated := strings.Replace(ruleText, r.use, "REMOVED_TRIGGER", 1)
		if mutated == ruleText {
			t.Fatalf("mutation target %q did not match rules/fyr-features.md", r.use)
		}
		if len(featureRoutingRuleProblems(mutated)) == 0 {
			t.Errorf("removing %q from rules/fyr-features.md did not fail featureRoutingRuleProblems", r.use)
		}
	}

	rows, found, err := ReadTrackedSuite(root, false)
	if err != nil || !found {
		t.Fatalf("ReadTrackedSuite: found=%v err=%v", found, err)
	}
	var got *Row
	for i := range rows {
		if rows[i].ID == "feature-routing" {
			got = &rows[i]
			break
		}
	}
	if got == nil {
		t.Fatal("GUIDE-eval.md is missing row feature-routing")
	}
	for _, want := range []string{"route: fyr search --slice", "fyr models --pick", "/fyr-work"} {
		if !strings.Contains(got.Check, want) {
			t.Errorf("feature-routing check missing %q:\n%s", want, got.Check)
		}
	}
}
