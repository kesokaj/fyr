package redact

import (
	"strings"
	"testing"
)

// needleNearMiss is one benign near-miss per prefix needle: the prefix one
// character off (a dash for an underscore, a version up, the mark missing),
// which must not be redacted (AGENTS.md § Rules: every needle gets a benign
// near-miss; SD40 of the 2026-10-27 audit).
var needleNearMiss = map[string]string{
	"-----BEGIN": "a ----BEGIN marker of four dashes", "PRIVATE KEY": "a private key file",
	"AKIA": "akia keys start with AKI", "ASIA": "the asia region", "AIza": "the AIz prefix",
	"ya29.": "ya29 tokens carry no dot", "GOCSPX-": "GOCSPX keys",
	"ghp_": "the ghp- prefix", "gho_": "the gho- prefix", "ghs_": "the ghs- prefix", "ghu_": "the ghu- prefix", "ghr_": "the ghr- prefix",
	"github_pat_": "github_pat tokens", "glpat-": "a glpat_ token", "gldt-": "gldt tokens", "glrt-": "glrt tokens", "glptt-": "glptt tokens",
	"sk-ant-": "sk-ant keys", "sk-proj-": "sk-proj keys",
	"whsec_":                      "a whsec is a webhook secret",
	"pypi-AgEIcHlwaS5vcmc":        "a pypi-AgEIcHlwaS5vcm prefix cut short",
	"pypi-AgENdGVzdC5weXBpLm9yZw": "a pypi-AgENdGVzdC5weXBpLm9yZ prefix cut short",
	"sk-svcacct-":                 "sk-svcacct keys", "sk-admin-": "sk-admin keys", "sk-None-": "sk-None keys",
	"AGE-SECRET-KEY-": "AGE-SECRET keys", "dop_v1_": "a dop_v2_ shape", "doo_v1_": "a doo_v2_ shape", "dor_v1_": "a dor_v2_ shape",
}

// Every prefix needle has a positive row in shapeSecrets (deleting a needle
// then fails a test) and a near-miss that stays (SD40 of the 2026-10-27
// audit: ten needles had no positive row, about fifteen no near-miss).
func TestEveryNeedleHasAShapeAndANearMiss(t *testing.T) {
	needles := map[string]bool{}
	for _, n := range prefixNeedles {
		needles[n] = true
		found := false
		for _, s := range shapeSecrets {
			if strings.Contains(s, n) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("needle %q has no positive row in shapeSecrets", n)
		}
		miss, ok := needleNearMiss[n]
		if !ok {
			t.Errorf("needle %q has no near-miss in needleNearMiss", n)
			continue
		}
		if _, recs := redactLines([]byte(miss+"\n"), false); len(recs) != 0 {
			t.Errorf("near-miss %q for %q is redacted: %+v", miss, n, recs)
		}
	}
	for n := range needleNearMiss {
		if !needles[n] {
			t.Errorf("needleNearMiss names %q, which is not a prefix needle", n)
		}
	}
}

// needlesIn names every needle, prefix or keyword, that fires on s; needleIn
// stops at the first.
func needlesIn(s string) []string {
	var hit []string
	for _, n := range prefixNeedles {
		if strings.Contains(s, n) {
			hit = append(hit, n)
		}
	}
	for _, k := range keywordNeedles {
		for _, m := range k.re.FindAllStringSubmatch(s, -1) {
			if k.accept == nil || k.accept(m) {
				hit = append(hit, k.pattern)
				break
			}
		}
	}
	return hit
}

// TestEveryKeywordNeedleBinds: every keyword needle has a shapeSecrets row
// that no other needle catches, so deleting any one of them fails
// TestRedactShapes (ledger D33 1; the Phase 7a security review's finding 2:
// `jwt short` caught every `jwt` row, and the `jwt` rule could be deleted
// with the suite green).
func TestEveryKeywordNeedleBinds(t *testing.T) {
	for _, k := range keywordNeedles {
		bound := false
		for _, s := range shapeSecrets {
			if hit := needlesIn(s); len(hit) == 1 && hit[0] == k.pattern {
				bound = true
				break
			}
		}
		if !bound {
			t.Errorf("keyword needle %q has no shapeSecrets row only it catches", k.pattern)
		}
	}
}
