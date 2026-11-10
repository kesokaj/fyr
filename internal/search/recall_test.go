package search

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type sliceBenchmarkCase struct {
	name        string
	query       string
	entryTerm   string
	goldSymbols []string
	chainFiles  []string
}

func seedMultiHopBenchmarkRepo(t *testing.T) (string, map[string]string) {
	t.Helper()
	root := newRepo(t)
	files := map[string]string{}

	padGo := func(pkg, prefix string, count int) string {
		var b strings.Builder
		for i := 1; i <= count; i++ {
			fmt.Fprintf(&b, "// %sHelper%02d performs auxiliary domain bookkeeping for %s.\n", prefix, i, pkg)
			fmt.Fprintf(&b, "func %sHelper%02d(input string, limit int) string {\n", prefix, i)
			fmt.Fprintf(&b, "\tif len(input) > limit && limit > 0 {\n\t\treturn input[:limit]\n\t}\n\treturn input\n}\n\n")
		}
		return b.String()
	}
	padTS := func(prefix string, count int) string {
		var b strings.Builder
		for i := 1; i <= count; i++ {
			fmt.Fprintf(&b, "// %sHelper%02d formats intermediate view state.\n", prefix, i)
			fmt.Fprintf(&b, "export function %sHelper%02d(label: string, count: number): string {\n", prefix, i)
			fmt.Fprintf(&b, "  return `${label}:${count}`;\n}\n\n")
		}
		return b.String()
	}
	padPy := func(prefix string, count int) string {
		var b strings.Builder
		for i := 1; i <= count; i++ {
			fmt.Fprintf(&b, "def %s_helper_%02d(payload: str, limit: int) -> str:\n", prefix, i)
			fmt.Fprintf(&b, "    \"\"\"Auxiliary helper %02d for pipeline stage.\"\"\"\n", i)
			fmt.Fprintf(&b, "    return payload[:limit] if limit > 0 else payload\n\n")
		}
		return b.String()
	}

	files["internal/api/transfer.go"] = "package api\n\nimport \"example.com/app/internal/ledger\"\n\n" +
		padGo("api", "TransferRoute", 22) +
		"func HandleTransfer(accountID string, amountCents int64) error {\n\treturn ledger.PostLedgerEntry(accountID, amountCents)\n}\n"
	files["internal/ledger/service.go"] = "package ledger\n\nimport \"example.com/app/internal/balance\"\n\n" +
		padGo("ledger", "Journal", 22) +
		"func PostLedgerEntry(accountID string, amountCents int64) error {\n\treturn balance.ComputeAccountDelta(accountID, amountCents)\n}\n"
	files["internal/balance/calc.go"] = "package balance\n\n" +
		padGo("balance", "Balance", 22) +
		"func ComputeAccountDelta(accountID string, amountCents int64) error {\n\t_ = accountID\n\t_ = amountCents\n\treturn nil\n}\n"

	files["internal/worker/dispatch.go"] = "package worker\n\nimport \"example.com/app/internal/retry\"\n\n" +
		padGo("worker", "Queue", 22) +
		"func DispatchRetryBatch(jobID string, attempt int) error {\n\treturn retry.ScheduleNextAttempt(jobID, attempt)\n}\n"
	files["internal/retry/backoff.go"] = "package retry\n\nimport \"example.com/app/internal/store\"\n\n" +
		padGo("retry", "Backoff", 22) +
		"func ScheduleNextAttempt(jobID string, attempt int) error {\n\treturn store.PersistDeadLetter(jobID, attempt)\n}\n"
	files["internal/store/dlq.go"] = "package store\n\n" +
		padGo("store", "DeadLetter", 22) +
		"func PersistDeadLetter(jobID string, attempt int) error {\n\t_ = jobID\n\t_ = attempt\n\treturn nil\n}\n"

	files["web/src/checkout.ts"] = "import { validateCartTotals } from './cart';\n\n" +
		padTS("checkoutView", 26) +
		"export function submitOrderCheckout(cartId: string, cents: number): string {\n  return validateCartTotals(cartId, cents);\n}\n"
	files["web/src/cart.ts"] = "import { formatCurrencyMinor } from './money';\n\n" +
		padTS("cartState", 26) +
		"export function validateCartTotals(cartId: string, cents: number): string {\n  return formatCurrencyMinor(cents);\n}\n"
	files["web/src/money.ts"] = padTS("moneyFormat", 26) +
		"export function formatCurrencyMinor(cents: number): string {\n  return `$${(cents / 100).toFixed(2)}`;\n}\n"

	files["pipeline/ingest.py"] = "from pipeline.transform import normalize_metric_point\n\n" +
		padPy("ingest_stage", 26) +
		"def ingest_telemetry_batch(series_id: str, value: float) -> str:\n    return normalize_metric_point(series_id, value)\n"
	files["pipeline/transform.py"] = "from pipeline.sink import flush_parquet_partition\n\n" +
		padPy("transform_stage", 26) +
		"def normalize_metric_point(series_id: str, value: float) -> str:\n    return flush_parquet_partition(series_id, value)\n"
	files["pipeline/sink.py"] = padPy("sink_stage", 26) +
		"def flush_parquet_partition(series_id: str, value: float) -> str:\n    return f\"{series_id}={value}\"\n"

	for rel, content := range files {
		writeFile(t, root, rel, content)
	}
	commitAll(t, root, "seed multi-hop recall benchmark repo")
	return root, files
}

func evalSymbolRecall(output string, goldSymbols []string) float64 {
	if len(goldSymbols) == 0 {
		return 0
	}
	hits := 0
	for _, sym := range goldSymbols {
		if strings.Contains(output, sym) {
			hits++
		}
	}
	return float64(hits) / float64(len(goldSymbols))
}

func simulateGrepPlusFullFileReads(root string, files map[string]string, term string) string {
	var b strings.Builder
	for rel, content := range files {
		if strings.Contains(content, term) {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
			if err == nil {
				b.Write(data)
			}
		}
	}
	return b.String()
}

func simulateFullChainReads(root string, chainFiles []string) string {
	var b strings.Builder
	for _, rel := range chainFiles {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err == nil {
			b.Write(data)
		}
	}
	return b.String()
}

func TestSliceVsGrepTokenEfficiencyAndRecall(t *testing.T) {
	root, files := seedMultiHopBenchmarkRepo(t)
	home := t.TempDir()

	// Validate evalSymbolRecall across must-pass (1.0), partial (0.67), and must-fail (0.0) states first (review-standards § 2).
	goldCheck := []string{"HandleTransfer", "PostLedgerEntry", "ComputeAccountDelta"}
	if r := evalSymbolRecall("func HandleTransfer() { PostLedgerEntry(); ComputeAccountDelta() }", goldCheck); r != 1.0 {
		t.Fatalf("evalSymbolRecall full match = %.2f, want 1.00", r)
	}
	if r := evalSymbolRecall("func HandleTransfer() { PostLedgerEntry() }", goldCheck); r >= 1.0 || r <= 0.5 {
		t.Fatalf("evalSymbolRecall partial match = %.2f, want 0.67", r)
	}
	if r := evalSymbolRecall("func unrelatedHelper() {}", goldCheck); r != 0.0 {
		t.Fatalf("evalSymbolRecall zero match = %.2f, want 0.00", r)
	}
	if r := evalSymbolRecall("func HandleTransfer() {}", nil); r != 0.0 {
		t.Fatalf("evalSymbolRecall empty gold = %.2f, want 0.00", r)
	}

	cases := []sliceBenchmarkCase{
		{
			name:        "Go 2-hop ledger transfer chain",
			query:       "How does HandleTransfer update account balances",
			entryTerm:   "HandleTransfer",
			goldSymbols: []string{"HandleTransfer", "PostLedgerEntry", "ComputeAccountDelta"},
			chainFiles:  []string{"internal/api/transfer.go", "internal/ledger/service.go", "internal/balance/calc.go"},
		},
		{
			name:        "Go 2-hop worker retry DLQ chain",
			query:       "Trace DispatchRetryBatch dead-letter scheduling",
			entryTerm:   "DispatchRetryBatch",
			goldSymbols: []string{"DispatchRetryBatch", "ScheduleNextAttempt", "PersistDeadLetter"},
			chainFiles:  []string{"internal/worker/dispatch.go", "internal/retry/backoff.go", "internal/store/dlq.go"},
		},
		{
			name:        "TypeScript 2-hop checkout currency chain",
			query:       "How does submitOrderCheckout validate and format totals",
			entryTerm:   "submitOrderCheckout",
			goldSymbols: []string{"submitOrderCheckout", "validateCartTotals", "formatCurrencyMinor"},
			chainFiles:  []string{"web/src/checkout.ts", "web/src/cart.ts", "web/src/money.ts"},
		},
		{
			name:        "Python 2-hop telemetry sink chain",
			query:       "Where does ingest_telemetry_batch flush partitions",
			entryTerm:   "ingest_telemetry_batch",
			goldSymbols: []string{"ingest_telemetry_batch", "normalize_metric_point", "flush_parquet_partition"},
			chainFiles:  []string{"pipeline/ingest.py", "pipeline/transform.py", "pipeline/sink.py"},
		},
	}

	var totalSliceBytes, totalGrepBytes, totalChainBytes int
	var sumSliceRecall, sumGrepRecall, sumChainRecall float64

	for _, tc := range cases {
		var sliceBuf bytes.Buffer
		if err := BuildContextSlice(Options{
			Dir:    root,
			Home:   home,
			Slice:  tc.query,
			Hops:   2,
			Budget: 600,
		}, &sliceBuf); err != nil {
			t.Fatalf("%s: BuildContextSlice: %v", tc.name, err)
		}
		sliceOut := sliceBuf.String()
		grepOut := simulateGrepPlusFullFileReads(root, files, tc.entryTerm)
		chainOut := simulateFullChainReads(root, tc.chainFiles)

		sliceRecall := evalSymbolRecall(sliceOut, tc.goldSymbols)
		grepRecall := evalSymbolRecall(grepOut, tc.goldSymbols)
		chainRecall := evalSymbolRecall(chainOut, tc.goldSymbols)

		sliceBytes := len(sliceOut)
		grepBytes := len(grepOut)
		chainBytes := len(chainOut)

		totalSliceBytes += sliceBytes
		totalGrepBytes += grepBytes
		totalChainBytes += chainBytes
		sumSliceRecall += sliceRecall
		sumGrepRecall += grepRecall
		sumChainRecall += chainRecall

		grepRatio := float64(sliceBytes) / float64(max(1, grepBytes))
		chainRatio := float64(sliceBytes) / float64(max(1, chainBytes))
		t.Logf("%s: slice recall=%.2f (%dB) | 1-hop grep+read recall=%.2f (%dB, slice=%.1f%%) | full-chain read recall=%.2f (%dB, slice=%.1f%%)",
			tc.name, sliceRecall, sliceBytes, grepRecall, grepBytes, grepRatio*100, chainRecall, chainBytes, chainRatio*100)

		if sliceRecall < grepRecall || sliceRecall < chainRecall {
			t.Errorf("%s: slice recall %.2f < grep %.2f or full-chain %.2f\nslice:\n%s", tc.name, sliceRecall, grepRecall, chainRecall, sliceOut)
		}
		if grepRatio > 0.35 {
			t.Errorf("%s: slice bytes %d is %.1f%% of 1-hop grep+full-file read (%dB), want <= 35%%", tc.name, sliceBytes, grepRatio*100, grepBytes)
		}
		if chainRatio > 0.35 {
			t.Errorf("%s: slice bytes %d is %.1f%% of full-chain file reads (%dB), want <= 35%%", tc.name, sliceBytes, chainRatio*100, chainBytes)
		}
	}

	n := float64(len(cases))
	overallGrepRatio := float64(totalSliceBytes) / float64(max(1, totalGrepBytes))
	overallChainRatio := float64(totalSliceBytes) / float64(max(1, totalChainBytes))
	t.Logf("MEAN over %d multi-hop queries: slice recall=%.2f (%dB) vs grep+read recall=%.2f (%dB, %.1f%% bytes) vs full-chain read recall=%.2f (%dB, %.1f%% bytes)",
		len(cases), sumSliceRecall/n, totalSliceBytes, sumGrepRecall/n, totalGrepBytes, overallGrepRatio*100, sumChainRecall/n, totalChainBytes, overallChainRatio*100)
}
