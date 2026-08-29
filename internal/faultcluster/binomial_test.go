package faultcluster

import "testing"

func TestUpperTailPValue_UniformDistributionIsNotSignificant(t *testing.T) {
	// 8 active cores, 8 faults each -> 64 total, exactly uniform.
	// P(X >= 8 | n=64, p=1/8) should be well above 0.01: this is right
	// around the expected value, not a tail event.
	p := upperTailPValue(64, 8, 1.0/8)
	if p < 0.3 {
		t.Fatalf("expected a high p-value for an exactly-uniform outcome, got %v", p)
	}
}

func TestUpperTailPValue_MatchesADRWorkedExample(t *testing.T) {
	// ADR-0011's own example: 34 segfaults in 6 days, 31 on one physical
	// core, "probabilidad de que sea azar del 0,0001%". With, say, 16
	// active cores (a plausible active-core count for the i9-13900K after
	// offlining CPU 8/9), p = 1/16 — 31 of 34 landing on one core must be
	// an extreme tail event.
	p := upperTailPValue(34, 31, 1.0/16)
	if p > 0.0001 {
		t.Fatalf("expected an extremely small p-value matching the ADR's own example, got %v", p)
	}
}

func TestUpperTailPValue_MoreSamplesOnACoreIsAlwaysLessOrEquallyLikely(t *testing.T) {
	prev := 1.0
	for k := 1; k <= 20; k++ {
		p := upperTailPValue(20, k, 1.0/4)
		if p > prev+1e-9 {
			t.Fatalf("expected p-value to be non-increasing in k, but P(X>=%d)=%v > P(X>=%d)=%v", k, p, k-1, prev)
		}
		prev = p
	}
}

func TestUpperTailPValue_ZeroSamplesIsCertain(t *testing.T) {
	if p := upperTailPValue(10, 0, 0.5); p != 1 {
		t.Fatalf("expected P(X>=0)=1, got %v", p)
	}
}

func TestUpperTailPValue_MoreThanTotalIsImpossible(t *testing.T) {
	if p := upperTailPValue(5, 6, 0.5); p != 0 {
		t.Fatalf("expected P(X>=6 | n=5)=0, got %v", p)
	}
}

func TestUpperTailPValue_SingleActiveCoreAlwaysCertain(t *testing.T) {
	// p=1: every fault is guaranteed to land on the only active core.
	if p := upperTailPValue(10, 10, 1.0); p != 1 {
		t.Fatalf("expected certainty with a single active core, got %v", p)
	}
}
