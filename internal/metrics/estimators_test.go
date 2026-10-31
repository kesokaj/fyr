package metrics

import (
	"math"
	"testing"
)

// near is equality at four decimals, the precision show --json prints.
func near(a, b float64) bool { return math.Abs(a-b) < 5e-5 }

// Chen 2021's pass@k: n=4, c=3 gives pass@2 = 1 (only one trial failed, so
// any two drawn hold a pass); the plug-in 1 − (1 − c/n)^k gives 0.9375 and
// must fail; n=4, c=1 gives pass@2 = 1 − C(3,2)/C(4,2) = 0.5; k above n or
// n = 0 is not an estimate.
func TestPassAtK(t *testing.T) {
	cases := []struct {
		n, c, k int
		want    float64
		ok      bool
	}{
		{4, 3, 2, 1, true},
		{4, 1, 2, 0.5, true},
		{4, 1, 1, 0.25, true},
		{4, 0, 3, 0, true},
		{6, 6, 5, 1, true},
		{10, 3, 5, 1 - 1.0/12, true}, // C(7,5)/C(10,5) = 21/252
		{4, 3, 5, 0, false},
		{0, 0, 1, 0, false},
		{4, 3, 0, 0, false},
	}
	for _, c := range cases {
		got, ok := PassAtK(c.n, c.c, c.k)
		if ok != c.ok || (ok && !near(got, c.want)) {
			t.Errorf("PassAtK(%d, %d, %d) = %.4f, %v; want %.4f, %v", c.n, c.c, c.k, got, ok, c.want, c.ok)
		}
	}
}

// τ-bench's pass^k: n=4, c=3 gives pass^2 = (3/4)(2/3) = 0.5; the plug-in
// (c/n)^k gives 0.5625 and must fail; c below k is 0, never negative.
func TestPassPowK(t *testing.T) {
	cases := []struct {
		n, c, k int
		want    float64
		ok      bool
	}{
		{4, 3, 2, 0.5, true},
		{4, 3, 1, 0.75, true},
		{4, 3, 4, 0, true},
		{6, 6, 3, 1, true},
		{5, 2, 3, 0, true},
		{10, 7, 3, 7.0 / 24, true}, // C(7,3)/C(10,3) = 35/120
		{4, 3, 5, 0, false},
		{0, 0, 1, 0, false},
		{4, 3, 0, 0, false},
	}
	for _, c := range cases {
		got, ok := PassPowK(c.n, c.c, c.k)
		if ok != c.ok || (ok && !near(got, c.want)) {
			t.Errorf("PassPowK(%d, %d, %d) = %.4f, %v; want %.4f, %v", c.n, c.c, c.k, got, ok, c.want, c.ok)
		}
	}
	if plug := math.Pow(3.0/4, 2); near(plug, 0.5) {
		t.Fatal("the plug-in form equals the estimator on the pinned case: the pin proves nothing")
	}
}

// Wilson 95 %: the plan's three pinned intervals, an interval that never
// leaves [0, 1] at c = n or c = 0 (where the Wald interval collapses to a
// point and must fail), and (0, 0) at n = 0.
func TestWilson(t *testing.T) {
	cases := []struct {
		n, c   int
		lo, hi float64
	}{
		{5, 5, 0.5655, 1},
		{3, 0, 0, 0.5615},
		{6, 6, 0.6097, 1},
		{1, 1, 0.2065, 1},
		{4, 2, 0.1500, 0.8500},
		{0, 0, 0, 0},
	}
	for _, c := range cases {
		lo, hi := Wilson(c.n, c.c, 1.96)
		if !near(lo, c.lo) || !near(hi, c.hi) {
			t.Errorf("Wilson(%d, %d, 1.96) = (%.4f, %.4f), want (%.4f, %.4f)", c.n, c.c, lo, hi, c.lo, c.hi)
		}
		if lo < 0 || hi > 1 || lo > hi {
			t.Errorf("Wilson(%d, %d, 1.96) = (%.4f, %.4f) leaves [0, 1]", c.n, c.c, lo, hi)
		}
	}
}

// TestWilsonRate: the interval of a mean rate over n units, which the
// suite's line uses with each row's c/n as its score, so p need not be a
// whole count over n; rounding p·n to a count, as Wilson needs, must fail
// the first case: Wilson(2, 2) is (0.3424, 1).
func TestWilsonRate(t *testing.T) {
	cases := []struct {
		n      int
		p      float64
		lo, hi float64
	}{
		{2, 0.75, 0.1979, 0.9733},
		{3, 2.0 / 3, 0.2077, 0.9385},
		{5, 1, 0.5655, 1},
		{0, 0.5, 0, 0},
	}
	for _, c := range cases {
		lo, hi := WilsonRate(c.n, c.p, 1.96)
		if !near(lo, c.lo) || !near(hi, c.hi) {
			t.Errorf("WilsonRate(%d, %.4f, 1.96) = (%.4f, %.4f), want (%.4f, %.4f)", c.n, c.p, lo, hi, c.lo, c.hi)
		}
	}
}

// C_out: always-pass and always-fail rows are 1, a coin-flip row is 0, the
// mean over rows, 0 for no rows.
func TestOutcomeConsistency(t *testing.T) {
	cases := []struct {
		ps   []float64
		want float64
	}{
		{nil, 0},
		{[]float64{1}, 1},
		{[]float64{0}, 1},
		{[]float64{0.5}, 0},
		{[]float64{1, 0.5}, 0.5},
		{[]float64{0.75, 0.25, 1}, (0.25 + 0.25 + 1) / 3},
	}
	for _, c := range cases {
		if got := OutcomeConsistency(c.ps); !near(got, c.want) {
			t.Errorf("OutcomeConsistency(%v) = %.4f, want %.4f", c.ps, got, c.want)
		}
	}
}

// Trajectory similarity: one substitution in two is 0.5 (a set overlap,
// Jaccard 1/3, must fail); order counts; an insertion costs one; two empty
// sequences are identical.
func TestSeqSimilarity(t *testing.T) {
	cases := []struct {
		a, b []string
		want float64
	}{
		{[]string{"Read", "Bash"}, []string{"Read", "Edit"}, 0.5},
		{[]string{"Read", "Bash"}, []string{"Bash", "Read"}, 0},
		{[]string{"Read", "Bash", "Edit"}, []string{"Read", "Edit"}, 2.0 / 3},
		{[]string{"Read"}, []string{"Read"}, 1},
		{nil, nil, 1},
		{[]string{"Read"}, nil, 0},
		{[]string{"a", "b", "c", "d"}, []string{"a", "x", "c", "d", "e"}, 0.6},
	}
	for _, c := range cases {
		if got := SeqSimilarity(c.a, c.b); !near(got, c.want) {
			t.Errorf("SeqSimilarity(%q, %q) = %.4f, want %.4f", c.a, c.b, got, c.want)
		}
		if back := SeqSimilarity(c.b, c.a); !near(back, c.want) {
			t.Errorf("SeqSimilarity(%q, %q) = %.4f, not symmetric", c.b, c.a, back)
		}
	}
}
