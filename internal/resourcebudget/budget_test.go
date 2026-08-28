package resourcebudget

import "testing"

func TestCheckBudget_AcceptsWithinBudget(t *testing.T) {
	if err := CheckBudget(30*1024*1024, 0.01); err != nil {
		t.Fatalf("expected within-budget values to pass, got %v", err)
	}
}

func TestCheckBudget_RejectsExcessRSS(t *testing.T) {
	if err := CheckBudget(100*1024*1024, 0.01); err == nil {
		t.Fatal("expected excess RSS to be rejected")
	}
}

func TestCheckBudget_RejectsExcessCPU(t *testing.T) {
	if err := CheckBudget(10*1024*1024, 0.5); err == nil {
		t.Fatal("expected excess CPU fraction to be rejected")
	}
}

func TestCheckBudget_AcceptsExactlyAtBudget(t *testing.T) {
	if err := CheckBudget(MaxRSSBytes, MaxCPUFraction); err != nil {
		t.Fatalf("expected values exactly at budget to pass, got %v", err)
	}
}
