package main

import "testing"

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
