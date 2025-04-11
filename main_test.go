package main

import (
	"math"
	"testing"
)

func buildApplicant(id, need string, score, requested float64) *applicant {
	return &applicant{
		ID:        id,
		NeedLevel: need,
		ScoreRaw:  score,
		Requested: requested,
		Eligible:  true,
	}
}

func prepApplicants(applicants []*applicant, scoreWeight, needWeight float64) {
	applyMinScore(applicants, 0)
	normalizeScores(applicants)
	assignPriority(applicants, scoreWeight, needWeight)
	sortApplicants(applicants)
}

func TestReserveLowGuaranteesLowNeedFunding(t *testing.T) {
	applicants := []*applicant{
		buildApplicant("high-1", "high", 99, 1000),
		buildApplicant("low-1", "low", 40, 1000),
	}
	prepApplicants(applicants, 0.7, 0.3)

	awarded := allocateBudget(applicants, 1000, 1000, 1000, 0, 0, 1, 0, 1)
	if len(awarded) != 1 {
		t.Fatalf("expected 1 awarded applicant, got %d", len(awarded))
	}
	if applicants[1].Awarded != 1000 {
		t.Fatalf("expected low-need applicant to be funded, got %.2f", applicants[1].Awarded)
	}
	if applicants[0].Awarded != 0 {
		t.Fatalf("expected high-need applicant to be unfunded, got %.2f", applicants[0].Awarded)
	}
}

func TestReserveMixAllocatesAcrossNeedLevels(t *testing.T) {
	applicants := []*applicant{
		buildApplicant("high-1", "high", 95, 1000),
		buildApplicant("high-2", "high", 90, 1000),
		buildApplicant("medium-1", "medium", 80, 1000),
		buildApplicant("low-1", "low", 60, 1000),
	}
	prepApplicants(applicants, 0.7, 0.3)

	awarded := allocateBudget(applicants, 4000, 1000, 1000, 0.5, 0.25, 0, 0, 1)
	if len(awarded) != 4 {
		t.Fatalf("expected 4 awarded applicants, got %d", len(awarded))
	}
	for _, item := range applicants {
		if item.Awarded != 1000 {
			t.Fatalf("expected %s to be fully funded, got %.2f", item.ID, item.Awarded)
		}
	}
}

func TestParseBudgetList(t *testing.T) {
	budgets, err := parseBudgetList("1000, 2500,5000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(budgets) != 3 {
		t.Fatalf("expected 3 budgets, got %d", len(budgets))
	}
	if budgets[0] != 1000 || budgets[1] != 2500 || budgets[2] != 5000 {
		t.Fatalf("unexpected budgets: %#v", budgets)
	}

	_, err = parseBudgetList("abc")
	if err == nil {
		t.Fatalf("expected error for invalid budget")
	}
}

func TestScenarioResultsBudgetImpact(t *testing.T) {
	applicants := []*applicant{
		buildApplicant("high-1", "high", 95, 1000),
		buildApplicant("low-1", "low", 80, 1000),
	}
	prepApplicants(applicants, 0.7, 0.3)

	results := buildScenarioResults(applicants, []float64{1000, 2000}, 1000, 1000, 0, 0, 0, 0, 1)
	if len(results) != 2 {
		t.Fatalf("expected 2 scenario results, got %d", len(results))
	}
	if results[0].AwardedCount != 1 || results[0].EligibleUnfundedCount != 1 {
		t.Fatalf("unexpected scenario 1 metrics: %#v", results[0])
	}
	if !floatEquals(results[0].CoverageRate, 0.5) {
		t.Fatalf("expected 0.5 coverage, got %.2f", results[0].CoverageRate)
	}

	if results[1].AwardedCount != 2 || results[1].EligibleUnfundedCount != 0 {
		t.Fatalf("unexpected scenario 2 metrics: %#v", results[1])
	}
	if !floatEquals(results[1].CoverageRate, 1.0) {
		t.Fatalf("expected 1.0 coverage, got %.2f", results[1].CoverageRate)
	}
}

func floatEquals(a, b float64) bool {
	return math.Abs(a-b) < 1e-6
}
