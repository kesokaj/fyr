package search

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestContractsGoAndTSSignaturesByFileDirAndSymbol(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()

	writeFile(t, root, "pkg/api/service.go", `package api

const DefaultTimeout = 30
const internalLimit = 5

type OrderStore interface {
	SaveOrder(id string) error
	deleteInternal(id string) error
}

type hiddenRunner interface {
	RunHidden() error
}

type OrderService struct {
	Store OrderStore
}

func NewOrderService(store OrderStore) *OrderService {
	return &OrderService{Store: store}
}

func (s *OrderService) HandleOrder(id string) error {
	return s.Store.SaveOrder(id)
}

func (s *OrderService) helperStep() {}

func parseInternal(raw string) string { return raw }
`)
	writeFile(t, root, "web/client.ts", `export interface OrderPayload {
  id: string;
}
export type OrderStatus = "open" | "closed";
export class OrderClient {
  send(p: OrderPayload): void {}
}
export async function fetchOrder(id: string): Promise<OrderPayload> {
  return { id };
}
export const API_VERSION = "v1";
const privateHelper = "nope";
function internalFormat(x: string): string {
  return x;
}
`)
	commitAll(t, root, "seed go and ts signatures")

	runContracts := func(target string) string {
		t.Helper()
		var buf bytes.Buffer
		if err := Search(Options{Dir: root, Home: home, Contracts: target}, &buf); err != nil {
			t.Fatalf("Search(--contracts %q): %v", target, err)
		}
		return buf.String()
	}

	// 1. Exact Go file path: exported symbols only, deterministic across runs.
	goOut1 := runContracts("pkg/api/service.go")
	goOut2 := runContracts("pkg/api/service.go")
	if goOut1 != goOut2 {
		t.Fatalf("non-deterministic output:\n--- run 1 ---\n%s\n--- run 2 ---\n%s", goOut1, goOut2)
	}
	if !strings.HasPrefix(goOut1, "# contracts: pkg/api/service.go (budget: 1500 tokens)\n") {
		t.Errorf("unexpected header:\n%s", goOut1)
	}
	for _, want := range []string{
		"[CODE_SIGNATURES]",
		"DefaultTimeout",
		"type OrderStore interface",
		"SaveOrder(id string) error",
		"type OrderService struct",
		"NewOrderService(store OrderStore) *OrderService",
		"HandleOrder(id string) error",
	} {
		if !strings.Contains(goOut1, want) {
			t.Errorf("Go file contracts missing %q:\n%s", want, goOut1)
		}
	}
	for _, forbid := range []string{"internalLimit", "deleteInternal", "RunHidden", "helperStep", "parseInternal"} {
		if strings.Contains(goOut1, forbid) {
			t.Errorf("unexported symbol %q leaked into contracts:\n%s", forbid, goOut1)
		}
	}

	// 2. Directory prefix with and without trailing slash.
	dirNoSlash := runContracts("pkg/api")
	dirSlash := runContracts("pkg/api/")
	if !strings.Contains(dirNoSlash, "HandleOrder") || !strings.Contains(dirSlash, "HandleOrder") {
		t.Errorf("directory prefix queries missed HandleOrder:\nno-slash:\n%s\nslash:\n%s", dirNoSlash, dirSlash)
	}

	// 3. Exact TypeScript file path.
	tsOut := runContracts("web/client.ts")
	for _, want := range []string{"OrderPayload", "OrderStatus", "OrderClient", "fetchOrder", "API_VERSION"} {
		if !strings.Contains(tsOut, want) {
			t.Errorf("TS contracts missing %q:\n%s", want, tsOut)
		}
	}
	for _, forbid := range []string{"privateHelper", "internalFormat"} {
		if strings.Contains(tsOut, forbid) {
			t.Errorf("unexported TS symbol %q leaked:\n%s", forbid, tsOut)
		}
	}

	// 4. Symbol name target (Go struct includes exported methods; TS class/func).
	symOut := runContracts("OrderService")
	if !strings.Contains(symOut, "type OrderService struct") || !strings.Contains(symOut, "HandleOrder") {
		t.Errorf("symbol query OrderService missed struct/method:\n%s", symOut)
	}
	tsSymOut := runContracts("fetchOrder")
	if !strings.Contains(tsSymOut, "fetchOrder") {
		t.Errorf("symbol query fetchOrder missed TS export:\n%s", tsSymOut)
	}
}

func TestContractsSpecSectionsFaultsTSVAndGuardrails(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "internal/pay/charge.go", "package pay\n\nfunc Charge(amount int) error { return nil }\n")
	commitAll(t, root, "seed pay package")

	specSmallBody := strings.Join([]string{
		"# SPEC-pay-core",
		"## Purpose",
		"Handles deterministic payment charges for `internal/pay/charge.go`.",
		"## Interfaces",
		"```go",
		"type ChargeRequest struct {",
		"\tAmount int",
		"}",
		"// ## Not A Real Heading",
		"```",
		"## Data",
		"Prose line ignored.",
		"- Amount MUST be > 0.",
		"* Currency uses ISO-4217 uppercase.",
		"  - Sub-invariant stays readable.",
		"```text",
		"- Bullet inside code fence must be ignored.",
		"```",
		"## Failure modes",
		"| Condition | Trigger | Behavior | Recovery |",
		"| --- | --- | --- | --- |",
		"| Zero amount | amount <= 0 | Return ErrInvalidAmount | Reject input |",
		"| Gateway timeout | > 5s | Return ErrTimeout | Retry idempotently |",
		"## Verification",
		"- Run `go test ./internal/pay/...`.",
	}, "\n")

	var bigRows strings.Builder
	bigRows.WriteString("## Interfaces\n```ts\nexport function settle(id: string): boolean\n```\n")
	bigRows.WriteString("## Data\n- Idempotency key is unique per ledger.\n")
	bigRows.WriteString("## Failure modes\n| Condition | Trigger | Behavior | Recovery |\n| --- | --- | --- | --- |\n")
	for i := 1; i <= 15; i++ {
		fmt.Fprintf(&bigRows, "| Err%02d | Cause%02d | Action%02d | Fix%02d |\n", i, i, i, i)
	}

	sources := []SpecContractSource{
		{
			ID: "SPEC-pay-z-big", Kind: "spec", Status: "current", Title: "Settlement faults",
			Body: bigRows.String(),
			ADRs: []SpecADRRef{
				{ID: "ADR-020", Status: "accepted", Title: "Ledger idempotency"},
				{ID: "ADR-005", Status: "superseded", Title: "Old superseded decision"},
			},
		},
		{
			ID: "SPEC-pay-old", Kind: "spec", Status: "superseded", Title: "Superseded spec",
			Body: "## Data\n- Must never appear.\n",
		},
		{
			ID: "SPEC-pay-a-core", Kind: "spec", Status: "current", Title: "Core charge contract",
			Body: specSmallBody,
			ADRs: []SpecADRRef{
				{ID: "ADR-010", Status: "accepted", Title: "Pure Go stdlib boundary"},
			},
		},
		{ID: "ADR-015", Kind: "adr", Status: "accepted", Title: "Strict retry backoff"},
	}

	opts := Options{
		Dir: root, Home: home, Contracts: "internal/pay/charge.go",
		BrainSpecBodies: func(_, _ string, _ []string) []SpecContractSource { return sources },
	}
	var b1, b2 bytes.Buffer
	if err := Search(opts, &b1); err != nil {
		t.Fatalf("Search run 1: %v", err)
	}
	if err := Search(opts, &b2); err != nil {
		t.Fatalf("Search run 2: %v", err)
	}
	out := b1.String()
	if out != b2.String() {
		t.Fatalf("non-deterministic spec projection:\n%s\nvs\n%s", out, b2.String())
	}

	// Deterministic spec ordering: SPEC-pay-a-core before SPEC-pay-z-big.
	idxA := strings.Index(out, "[SPEC_CONTRACTS] (SPEC-pay-a-core)")
	idxZ := strings.Index(out, "[SPEC_CONTRACTS] (SPEC-pay-z-big)")
	if idxA < 0 || idxZ < 0 || idxA >= idxZ {
		t.Fatalf("expected sorted SPEC_CONTRACTS headers (A before Z):\n%s", out)
	}

	for _, want := range []string{
		"type ChargeRequest struct {",
		"Amount int",
		"[INVARIANTS] (SPEC-pay-a-core)",
		"- Amount MUST be > 0.",
		"- Currency uses ISO-4217 uppercase.",
		"[FAULTS] (SPEC-pay-a-core)",
		"| Zero amount | amount <= 0 | Return ErrInvalidAmount | Reject input |",
		"[FAULTS] (SPEC-pay-z-big)",
		"faults[15]{Condition,Trigger,Behavior,Recovery}:",
		"Err01\tCause01\tAction01\tFix01",
		"Err15\tCause15\tAction15\tFix15",
		"[GUARDRAILS]",
		"- ADR-010: Pure Go stdlib boundary",
		"- ADR-015: Strict retry backoff",
		"- ADR-020: Ledger idempotency",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("contracts output missing %q:\n%s", want, out)
		}
	}

	// Guardrails sorted: ADR-010 < ADR-015 < ADR-020.
	i10 := strings.Index(out, "- ADR-010:")
	i15 := strings.Index(out, "- ADR-015:")
	i20 := strings.Index(out, "- ADR-020:")
	if !(i10 < i15 && i15 < i20) {
		t.Errorf("guardrails not sorted by ADR ID:\n%s", out)
	}

	// Excluded content: superseded spec/ADR, prose in ## Data, bullet inside code fence.
	for _, forbid := range []string{
		"Must never appear",
		"ADR-005",
		"Prose line ignored",
		"Bullet inside code fence must be ignored",
	} {
		if strings.Contains(out, forbid) {
			t.Errorf("unwanted text %q leaked into projection:\n%s", forbid, out)
		}
	}
}

func TestContractsBudgetUTF8SanitizationAndErrors(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "pkg/utf/mod.go", "package utf\n\nfunc ProcessUTF8(s string) string { return s }\n")
	commitAll(t, root, "seed utf repo")

	// 1. UTF-8 safe budget truncation & Printable sanitization.
	ansiAndBidi := "\x1b[31mRED\x1b[0m \u202eSPOOF\u200b \x07BEL"
	longUTF8 := strings.Repeat("Invariant — arrow → check 日本語 · ", 30)
	var tsvRows strings.Builder
	tsvRows.WriteString("## Data\n- " + ansiAndBidi + "\n- " + longUTF8 + "\n")
	tsvRows.WriteString("## Failure modes\n| Cond | Trig | Beh | Rec |\n| --- | --- | --- | --- |\n")
	for i := 1; i <= 15; i++ {
		fmt.Fprintf(&tsvRows, "| C%02d\x1b[31m | T%02d | B%02d\u202e | R%02d |\n", i, i, i, i)
	}

	var buf bytes.Buffer
	err := Search(Options{
		Dir: root, Home: home, Contracts: "pkg/utf/mod.go", Budget: 60, // 60*4 = 240 bytes
		BrainSpecBodies: func(_, _ string, _ []string) []SpecContractSource {
			return []SpecContractSource{{
				ID: "SPEC-utf", Kind: "spec", Status: "current", Title: "UTF " + ansiAndBidi,
				Body: tsvRows.String(),
				ADRs: []SpecADRRef{{ID: "ADR-001", Status: "accepted", Title: "Title " + ansiAndBidi}},
			}}
		},
	}, &buf)
	if err != nil {
		t.Fatalf("Search(--budget 60): %v", err)
	}
	got := buf.String()
	if len(got) > 240 {
		t.Errorf("budget 60 exceeded 240 bytes: len=%d\n%s", len(got), got)
	}
	if !utf8.ValidString(got) {
		t.Errorf("truncated contracts output is invalid UTF-8: %q", got)
	}
	if !strings.HasPrefix(got, "# contracts: pkg/utf/mod.go (budget: 60 tokens)\n") {
		t.Errorf("budget header mismatch:\n%s", got)
	}

	// Full budget run to verify ANSI/control/bidi stripping across all sections & TSV tabs intact.
	buf.Reset()
	err = Search(Options{
		Dir: root, Home: home, Contracts: "pkg/utf/mod.go", Budget: 1500,
		BrainSpecBodies: func(_, _ string, _ []string) []SpecContractSource {
			return []SpecContractSource{{
				ID: "SPEC-utf", Kind: "spec", Status: "current", Title: "UTF " + ansiAndBidi,
				Body: tsvRows.String(),
				ADRs: []SpecADRRef{{ID: "ADR-001", Status: "accepted", Title: "Title " + ansiAndBidi}},
			}}
		},
	}, &buf)
	if err != nil {
		t.Fatalf("Search(full): %v", err)
	}
	full := buf.String()
	for _, bad := range []string{"\x1b", "\u202e", "\u200b", "\x07"} {
		if strings.Contains(full, bad) {
			t.Errorf("control/bidi byte %q leaked into output:\n%q", bad, full)
		}
	}
	if !strings.Contains(full, "faults[15]{Cond,Trig,Beh,Rec}:") || !strings.Contains(full, "\tT01\t") {
		t.Errorf("expected sanitized TSV fault rows with real tab separators:\n%s", full)
	}

	// 2. Must-fail: target with no indexed symbols and no linked SPEC-*.
	buf.Reset()
	err = Search(Options{Dir: root, Home: home, Contracts: "does/not/exist.go"}, &buf)
	if err == nil || IsUsage(err) {
		t.Fatalf("missing target: err = %v (IsUsage=%v), want non-usage error", err, IsUsage(err))
	}
	if want := "search: no indexed symbols or linked specs for does/not/exist.go"; err.Error() != want {
		t.Errorf("missing target err = %q, want %q", err.Error(), want)
	}

	// 3. Must-fail: mutual exclusion with every other search mode -> usageError naming --contracts.
	conflicts := []struct {
		name string
		opts Options
	}{
		{"words", Options{Dir: root, Home: home, Contracts: "pkg/utf/mod.go", Words: []string{"extra"}}},
		{"refs", Options{Dir: root, Home: home, Contracts: "pkg/utf/mod.go", Refs: "ADR-001"}},
		{"open", Options{Dir: root, Home: home, Contracts: "pkg/utf/mod.go", Open: true}},
		{"slice", Options{Dir: root, Home: home, Contracts: "pkg/utf/mod.go", Slice: "ProcessUTF8"}},
		{"skeleton", Options{Dir: root, Home: home, Contracts: "pkg/utf/mod.go", Skeleton: "pkg/utf/mod.go"}},
		{"path", Options{Dir: root, Home: home, Contracts: "pkg/utf/mod.go", Path: "A", To: "B"}},
		{"affected", Options{Dir: root, Home: home, Contracts: "pkg/utf/mod.go", Affected: "pkg/utf/mod.go"}},
		{"map", Options{Dir: root, Home: home, Contracts: "pkg/utf/mod.go", Map: true}},
		{"vet", Options{Dir: root, Home: home, Contracts: "pkg/utf/mod.go", Vet: "check"}},
		{"hook", Options{Dir: root, Home: home, Contracts: "pkg/utf/mod.go", Hook: "pre-invocation"}},
	}
	for _, tc := range conflicts {
		t.Run("conflict_"+tc.name, func(t *testing.T) {
			err := Validate(tc.opts)
			if err == nil || !IsUsage(err) || !strings.Contains(err.Error(), "--contracts") {
				t.Errorf("%s: Validate = %v (IsUsage=%v), want usageError naming --contracts", tc.name, err, IsUsage(err))
			}
		})
	}
}

func TestContractsRootTargetAndEscapedPipeTable(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "pkg/root/api.go", "package root\n\nfunc ExportedRoot() error { return nil }\n")
	writeFile(t, root, "pkg/root/api_test.go", "package root\n\nfunc ExportedTestOnly() {}\n")
	commitAll(t, root, "seed root target")

	specBody := "## Failure modes\nTrigger | Observable | Retry | Action\n--- | --- | --- | ---\nA | ends with pipe \\| | 0 | keep literal \\|\n"
	sources := []SpecContractSource{{
		ID: "SPEC-pipe", Kind: "spec", Status: "current", Title: "Pipe edge",
		Body: specBody,
	}}
	for _, target := range []string{".", "./"} {
		var buf bytes.Buffer
		err := Search(Options{
			Dir: root, Home: home, Contracts: target,
			BrainSpecBodies: func(_, _ string, _ []string) []SpecContractSource { return sources },
		}, &buf)
		if err != nil {
			t.Fatalf("Search(--contracts %q): %v", target, err)
		}
		out := buf.String()
		if !strings.Contains(out, "ExportedRoot") || strings.Contains(out, "ExportedTestOnly") {
			t.Errorf("target %q unexpected symbols:\n%s", target, out)
		}
		if !strings.Contains(out, "keep literal |") {
			t.Errorf("target %q dropped escaped trailing pipe in Failure modes:\n%s", target, out)
		}
	}
}
