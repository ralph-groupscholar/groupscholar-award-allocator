package main

import "testing"

func TestComputeAward(t *testing.T) {
	award := computeAward(1000, 500, 2000, 0, 0.5)
	if award != 500 {
		t.Fatalf("expected award 500, got %.2f", award)
	}

	award = computeAward(300, 500, 2000, 0, 1)
	if award != 300 {
		t.Fatalf("expected award 300 when requested below min, got %.2f", award)
	}

	award = computeAward(1100, 200, 2000, 250, 1)
	if award != 1000 {
		t.Fatalf("expected rounded award 1000, got %.2f", award)
	}
}

func TestAllocateBudgetReserves(t *testing.T) {
	applicants := []*applicant{
		{ID: "a1", Name: "A", NeedLevel: "high", ScoreRaw: 95, Requested: 300, Eligible: true},
		{ID: "a2", Name: "B", NeedLevel: "high", ScoreRaw: 90, Requested: 300, Eligible: true},
		{ID: "a3", Name: "C", NeedLevel: "low", ScoreRaw: 85, Requested: 300, Eligible: true},
		{ID: "a4", Name: "D", NeedLevel: "low", ScoreRaw: 80, Requested: 300, Eligible: true},
	}

	normalizeScores(applicants)
	assignPriority(applicants, 0.7, 0.3)
	sortApplicants(applicants)

	awarded := allocateBudget(applicants, 1000, 100, 300, 0.5, 0, 0, 0, 1)
	if len(awarded) != 4 {
		t.Fatalf("expected 4 awards, got %d", len(awarded))
	}

	if total := totalAwarded(awarded); total != 1000 {
		t.Fatalf("expected total awarded 1000, got %.2f", total)
	}

	var highTotal float64
	for _, item := range awarded {
		if item.NeedLevel == "high" {
			highTotal += item.Awarded
		}
	}
	if highTotal != 500 {
		t.Fatalf("expected high-need total 500, got %.2f", highTotal)
	}
}

func TestSummarizeCounts(t *testing.T) {
	applicants := []*applicant{
		{ID: "1", NeedLevel: "high", ScoreRaw: 90, Requested: 1000, Eligible: true, Awarded: 1000},
		{ID: "2", NeedLevel: "low", ScoreRaw: 80, Requested: 500, Eligible: true, Awarded: 0},
		{ID: "3", NeedLevel: "medium", ScoreRaw: 70, Requested: 300, Eligible: true, Awarded: 200},
		{ID: "4", NeedLevel: "low", ScoreRaw: 60, Requested: 0, Eligible: false, EligibilityMsg: "requested_amount must be > 0"},
	}

	awarded := []*applicant{applicants[0], applicants[2]}
	summary := summarize(applicants, 1500, awarded)

	if summary.EligibleCount != 3 {
		t.Fatalf("expected eligible count 3, got %d", summary.EligibleCount)
	}
	if summary.AwardedCount != 2 {
		t.Fatalf("expected awarded count 2, got %d", summary.AwardedCount)
	}
	if summary.EligibleUnfundedCount != 1 {
		t.Fatalf("expected eligible unfunded count 1, got %d", summary.EligibleUnfundedCount)
	}
	if summary.IneligibleCount != 1 {
		t.Fatalf("expected ineligible count 1, got %d", summary.IneligibleCount)
	}
	if summary.FullyFundedCount != 1 {
		t.Fatalf("expected fully funded count 1, got %d", summary.FullyFundedCount)
	}
	if summary.PartiallyFundedCount != 1 {
		t.Fatalf("expected partially funded count 1, got %d", summary.PartiallyFundedCount)
	}
	if summary.FundingGapTotal != 600 {
		t.Fatalf("expected funding gap 600, got %.2f", summary.FundingGapTotal)
	}
	if summary.BudgetRequiredFull != 1800 {
		t.Fatalf("expected budget required 1800, got %.2f", summary.BudgetRequiredFull)
	}
	if summary.BudgetShortfall != 300 {
		t.Fatalf("expected budget shortfall 300, got %.2f", summary.BudgetShortfall)
	}

	highCoverage := summary.NeedCoverage["high"]
	if !floatApprox(highCoverage.RequestedShare, 1000.0/1800.0, 0.0001) {
		t.Fatalf("expected high requested share %.4f, got %.4f", 1000.0/1800.0, highCoverage.RequestedShare)
	}
	if !floatApprox(highCoverage.AwardedShare, 1000.0/1200.0, 0.0001) {
		t.Fatalf("expected high awarded share %.4f, got %.4f", 1000.0/1200.0, highCoverage.AwardedShare)
	}
}

func floatApprox(value, expected, epsilon float64) bool {
	if value > expected {
		return value-expected <= epsilon
	}
	return expected-value <= epsilon
}
