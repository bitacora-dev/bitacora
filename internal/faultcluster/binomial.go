package faultcluster

import "math"

// upperTailPValue computes P(X >= k) for X ~ Binomial(n, p) — the
// one-sided test ADR-0011 calls for: "la probabilidad de que sea azar",
// i.e. of seeing at least this many faults on one core if faults were
// actually spread uniformly across every active core.
//
// Computed via the standard successive-ratio recurrence
// (term_i = term_{i-1} * (n-i+1)/i * p/(1-p)) rather than raw binomial
// coefficients, so it stays numerically stable without needing
// arbitrary-precision arithmetic for the sample sizes this ever sees
// (ADR-0011's own example: 34 segfaults in 6 days).
func upperTailPValue(n, k int, p float64) float64 {
	if k <= 0 {
		return 1
	}
	if n <= 0 || k > n {
		return 0
	}
	if p <= 0 {
		return 0
	}
	if p >= 1 {
		return 1
	}

	q := 1 - p
	term := math.Pow(q, float64(n)) // P(X = 0)
	sum := 0.0
	for i := 0; i <= n; i++ {
		if i > 0 {
			term *= float64(n-i+1) / float64(i) * p / q
		}
		if i >= k {
			sum += term
		}
	}
	if sum > 1 {
		sum = 1
	}
	return sum
}
