package metrics

import "math"

// The estimators behind pass^k and the per-row view (the eval-metrics plan
// D2, D3): stdlib arithmetic only (R4), each pinned to a known answer in
// estimators_test.go together with the plug-in form it must not degrade to.

// PassAtK is Chen et al. 2021's unbiased pass@k over n graded trials of
// which c passed: the chance that at least one of k trials drawn without
// replacement passed, 1 − C(n−c, k)/C(n, k), in the product form that does
// not overflow. ok is false when there is nothing to estimate (n is 0, or
// k is outside 1..n).
func PassAtK(n, c, k int) (float64, bool) {
	if n <= 0 || k <= 0 || k > n {
		return 0, false
	}
	if n-c < k {
		return 1, true // fewer failures than draws: every draw holds a pass
	}
	miss := 1.0
	for i := n - c + 1; i <= n; i++ {
		miss *= 1 - float64(k)/float64(i)
	}
	return 1 - miss, true
}

// PassPowK is τ-bench's pass^k over n graded trials of which c passed: the
// chance that all k trials drawn without replacement passed,
// C(c, k)/C(n, k) = ∏_{i=0}^{k−1} (c−i)/(n−i), 0 when c < k. ok is false
// when there is nothing to estimate (n is 0, or k is outside 1..n).
func PassPowK(n, c, k int) (float64, bool) {
	if n <= 0 || k <= 0 || k > n {
		return 0, false
	}
	if c < k {
		return 0, true
	}
	p := 1.0
	for i := 0; i < k; i++ {
		p *= float64(c-i) / float64(n-i)
	}
	return p, true
}

// Wilson is the Wilson score interval of c passes in n trials at the normal
// quantile z (1.96 for 95 %), clamped to [0, 1]; (0, 0) when n is 0.
func Wilson(n, c int, z float64) (lo, hi float64) {
	if n <= 0 {
		return 0, 0
	}
	nf := float64(n)
	p := float64(c) / nf
	z2 := z * z
	denom := 1 + z2/nf
	centre := (p + z2/(2*nf)) / denom
	half := z / denom * math.Sqrt(p*(1-p)/nf+z2/(4*nf*nf))
	return math.Max(0, centre-half), math.Min(1, centre+half)
}

// OutcomeConsistency is Rabanser et al. 2026's C_out over rows: the mean of
// (2p̂ − 1)² over each row's pass rate p̂, 1 when every row is always-pass or
// always-fail, 0 for no rows.
func OutcomeConsistency(ps []float64) float64 {
	if len(ps) == 0 {
		return 0
	}
	sum := 0.0
	for _, p := range ps {
		d := 2*p - 1
		sum += d * d
	}
	return sum / float64(len(ps))
}

// SeqSimilarity is the trajectory similarity of two tool sequences,
// 1 − Levenshtein(a, b)/max(len(a), len(b)); 1 for two empty sequences.
func SeqSimilarity(a, b []string) float64 {
	longest := max(len(a), len(b))
	if longest == 0 {
		return 1
	}
	return 1 - float64(levenshtein(a, b))/float64(longest)
}

// levenshtein is the edit distance between two sequences, counting an
// insertion, a deletion and a substitution as one each, in two rows.
func levenshtein(a, b []string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
