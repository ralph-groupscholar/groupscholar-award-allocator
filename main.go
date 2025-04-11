package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type applicant struct {
	ID             string
	Name           string
	NeedLevel      string
	ScoreRaw       float64
	ScoreNorm      float64
	Requested      float64
	PriorityScore  float64
	Awarded        float64
	Eligible       bool
	EligibilityMsg string
}

type allocationSummary struct {
	GeneratedAt             string                     `json:"generated_at"`
	Budget                  float64                    `json:"budget"`
	BudgetUsed              float64                    `json:"budget_used"`
	BudgetLeft              float64                    `json:"budget_left"`
	BudgetRequiredFull      float64                    `json:"budget_required_full"`
	BudgetShortfall         float64                    `json:"budget_shortfall"`
	Applicants              int                        `json:"applicants"`
	EligibleCount           int                        `json:"eligible_count"`
	AwardedCount            int                        `json:"awarded_count"`
	IneligibleCount         int                        `json:"ineligible_count"`
	EligibleUnfundedCount   int                        `json:"eligible_unfunded_count"`
	EligibleUnfundedAmount  float64                    `json:"eligible_unfunded_amount"`
	EligibleRequestedTotal  float64                    `json:"eligible_requested_total"`
	FullyFundedCount        int                        `json:"fully_funded_count"`
	PartiallyFundedCount    int                        `json:"partially_funded_count"`
	FundingGapTotal         float64                    `json:"funding_gap_total"`
	CoverageRate            float64                    `json:"coverage_rate"`
	FullFundingRate         float64                    `json:"full_funding_rate"`
	AverageAward            float64                    `json:"average_award"`
	AwardP25                float64                    `json:"award_p25"`
	AwardP50                float64                    `json:"award_p50"`
	AwardP75                float64                    `json:"award_p75"`
	AwardToRequestAvg       float64                    `json:"award_to_request_avg"`
	MinAwarded              float64                    `json:"min_awarded"`
	MaxAwarded              float64                    `json:"max_awarded"`
	LastFundedPriority      float64                    `json:"last_funded_priority"`
	LastFundedScore         float64                    `json:"last_funded_score"`
	LastFundedNeed          string                     `json:"last_funded_need"`
	LastFundedRequested     float64                    `json:"last_funded_requested"`
	ByNeed                  map[string]needAgg         `json:"by_need"`
	NeedCoverage            map[string]needCoverageAgg `json:"need_coverage"`
	UnfundedByNeed          map[string]needUnfundedAgg `json:"unfunded_by_need"`
	IneligibleReasonSummary map[string]int             `json:"ineligible_reasons"`
	Awards                  []awardRecord              `json:"awards"`
	Unfunded                []awardRecord              `json:"unfunded"`
	Ineligible              []ineligibleRecord         `json:"ineligible"`
	ScenarioResults         []scenarioResult           `json:"scenario_results,omitempty"`
}

type needAgg struct {
	AwardedCount int     `json:"awarded_count"`
	BudgetUsed   float64 `json:"budget_used"`
}

type needCoverageAgg struct {
	EligibleCount  int     `json:"eligible_count"`
	AwardedCount   int     `json:"awarded_count"`
	UnfundedCount  int     `json:"unfunded_count"`
	RequestedTotal float64 `json:"requested_total"`
	AwardedTotal   float64 `json:"awarded_total"`
	CoverageRate   float64 `json:"coverage_rate"`
	RequestedShare float64 `json:"requested_share"`
	AwardedShare   float64 `json:"awarded_share"`
	ShareDelta     float64 `json:"share_delta"`
}

type needUnfundedAgg struct {
	Count     int     `json:"count"`
	Requested float64 `json:"requested"`
}

type awardRecord struct {
	ApplicantID string  `json:"applicant_id"`
	Name        string  `json:"name"`
	NeedLevel   string  `json:"need_level"`
	Score       float64 `json:"score"`
	Requested   float64 `json:"requested"`
	Awarded     float64 `json:"awarded"`
	Priority    float64 `json:"priority"`
}

type ineligibleRecord struct {
	ApplicantID string  `json:"applicant_id"`
	Name        string  `json:"name"`
	NeedLevel   string  `json:"need_level"`
	Score       float64 `json:"score"`
	Requested   float64 `json:"requested"`
	Reason      string  `json:"reason"`
}

type scenarioResult struct {
	Budget                float64 `json:"budget"`
	BudgetUsed            float64 `json:"budget_used"`
	BudgetLeft            float64 `json:"budget_left"`
	BudgetRequiredFull    float64 `json:"budget_required_full"`
	AwardedCount          int     `json:"awarded_count"`
	EligibleCount         int     `json:"eligible_count"`
	EligibleUnfundedCount int     `json:"eligible_unfunded_count"`
	FullyFundedCount      int     `json:"fully_funded_count"`
	PartiallyFundedCount  int     `json:"partially_funded_count"`
	CoverageRate          float64 `json:"coverage_rate"`
	FullFundingRate       float64 `json:"full_funding_rate"`
	FundingGapTotal       float64 `json:"funding_gap_total"`
	AverageAward          float64 `json:"average_award"`
	AwardToRequestAvg     float64 `json:"award_to_request_avg"`
}

func main() {
	inputPath := flag.String("input", "", "Path to applicant CSV file")
	budget := flag.Float64("budget", 0, "Total award budget")
	minAward := flag.Float64("min", 500, "Minimum award amount")
	maxAward := flag.Float64("max", 5000, "Maximum award amount")
	scoreWeight := flag.Float64("score-weight", 0.7, "Weight for applicant score (0-1)")
	needWeight := flag.Float64("need-weight", 0.3, "Weight for need level (0-1)")
	reserveHigh := flag.Float64("reserve-high", 0, "Share of budget reserved for high-need applicants (0-1)")
	reserveMedium := flag.Float64("reserve-medium", 0, "Share of budget reserved for medium-need applicants (0-1)")
	reserveLow := flag.Float64("reserve-low", 0, "Share of budget reserved for low-need applicants (0-1)")
	roundTo := flag.Float64("round", 0, "Round awards to nearest increment (0 disables)")
	maxPercent := flag.Float64("max-percent", 1, "Max percent of requested amount to award (0-1]")
	minScore := flag.Float64("min-score", 0, "Minimum applicant score to be eligible")
	jsonPath := flag.String("json", "", "Optional path to write JSON output")
	awardsCSV := flag.String("awards-csv", "", "Optional path to write awarded applicants CSV")
	unfundedCSV := flag.String("unfunded-csv", "", "Optional path to write unfunded eligible applicants CSV")
	ineligibleCSV := flag.String("ineligible-csv", "", "Optional path to write ineligible applicants CSV")
	reportPath := flag.String("report", "", "Optional path to write Markdown allocation report")
	scenarioBudgets := flag.String("scenario-budgets", "", "Comma-separated budgets for scenario analysis")
	topN := flag.Int("top", 10, "Number of awarded applicants to display")
	showAll := flag.Bool("all", false, "Show all awarded applicants")
	unfundedTop := flag.Int("unfunded", 10, "Number of unfunded eligible applicants to display")
	showAllUnfunded := flag.Bool("unfunded-all", false, "Show all unfunded eligible applicants")
	dbLog := flag.Bool("db-log", false, "Log allocation run to Postgres when GS_AWARD_ALLOCATOR_DB_URL is set")
	flag.Parse()

	if *inputPath == "" || *budget <= 0 {
		exitWith("input and budget are required")
	}
	if *minAward < 0 || *maxAward <= 0 || *maxAward < *minAward {
		exitWith("invalid min/max award values")
	}
	if *scoreWeight < 0 || *needWeight < 0 {
		exitWith("weights must be non-negative")
	}
	if *reserveHigh < 0 || *reserveHigh > 1 {
		exitWith("reserve-high must be between 0 and 1")
	}
	if *reserveMedium < 0 || *reserveMedium > 1 {
		exitWith("reserve-medium must be between 0 and 1")
	}
	if *reserveLow < 0 || *reserveLow > 1 {
		exitWith("reserve-low must be between 0 and 1")
	}
	if *reserveHigh+*reserveMedium+*reserveLow > 1 {
		exitWith("reserve shares must sum to 1 or less")
	}
	if *roundTo < 0 {
		exitWith("round must be >= 0")
	}
	if *maxPercent <= 0 || *maxPercent > 1 {
		exitWith("max-percent must be between 0 (exclusive) and 1")
	}
	if *minScore < 0 {
		exitWith("min-score must be >= 0")
	}
	weightTotal := *scoreWeight + *needWeight
	if weightTotal == 0 {
		exitWith("score-weight and need-weight cannot both be zero")
	}
	scenarioList, err := parseBudgetList(*scenarioBudgets)
	if err != nil {
		exitWith(err.Error())
	}

	applicants, warnings, err := loadApplicants(*inputPath)
	if err != nil {
		exitWith(err.Error())
	}

	applyMinScore(applicants, *minScore)
	normalizeScores(applicants)
	assignPriority(applicants, *scoreWeight, *needWeight)
	sortApplicants(applicants)

	awarded := allocateBudget(applicants, *budget, *minAward, *maxAward, *reserveHigh, *reserveMedium, *reserveLow, *roundTo, *maxPercent)
	if len(warnings) > 0 {
		fmt.Println("Warnings:")
		for _, warning := range warnings {
			fmt.Printf("- %s\n", warning)
		}
		fmt.Println()
	}

	summary := summarize(applicants, *budget, awarded)
	if len(scenarioList) > 0 {
		summary.ScenarioResults = buildScenarioResults(applicants, scenarioList, *minAward, *maxAward, *reserveHigh, *reserveMedium, *reserveLow, *roundTo, *maxPercent)
	}
	printSummary(summary)
	printScenarioResults(summary.ScenarioResults)
	printAwards(awarded, *topN, *showAll)
	printUnfunded(summary.Unfunded, *unfundedTop, *showAllUnfunded)

	if *jsonPath != "" {
		if err := writeJSON(*jsonPath, summary, awarded); err != nil {
			exitWith(err.Error())
		}
		fmt.Printf("\nJSON written to %s\n", *jsonPath)
	}

	if *awardsCSV != "" {
		if err := writeAwardsCSV(*awardsCSV, awarded); err != nil {
			exitWith(err.Error())
		}
		fmt.Printf("\nAwarded CSV written to %s\n", *awardsCSV)
	}

	if *unfundedCSV != "" {
		if err := writeUnfundedCSV(*unfundedCSV, summary.Unfunded); err != nil {
			exitWith(err.Error())
		}
		fmt.Printf("\nUnfunded CSV written to %s\n", *unfundedCSV)
	}

	if *ineligibleCSV != "" {
		if err := writeIneligibleCSV(*ineligibleCSV, summary.Ineligible); err != nil {
			exitWith(err.Error())
		}
		fmt.Printf("\nIneligible CSV written to %s\n", *ineligibleCSV)
	}

	if *reportPath != "" {
		if err := writeReport(*reportPath, summary, *topN, *showAll, *unfundedTop, *showAllUnfunded); err != nil {
			exitWith(err.Error())
		}
		fmt.Printf("\nMarkdown report written to %s\n", *reportPath)
	}

	if *dbLog {
		dbConfig, err := loadDBConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "DB logging disabled: %v\n", err)
		} else if !dbConfig.Enabled {
			fmt.Fprintln(os.Stderr, "DB logging disabled: GS_AWARD_ALLOCATOR_DB_URL not set")
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			opts := dbRunOptions{
				MinAward:      *minAward,
				MaxAward:      *maxAward,
				ScoreWeight:   *scoreWeight,
				NeedWeight:    *needWeight,
				ReserveHigh:   *reserveHigh,
				ReserveMedium: *reserveMedium,
				ReserveLow:    *reserveLow,
				RoundTo:       *roundTo,
				MaxPercent:    *maxPercent,
				MinScore:      *minScore,
			}
			if err := logRunToDatabase(ctx, dbConfig, summary, applicants, *inputPath, opts); err != nil {
				fmt.Fprintf(os.Stderr, "DB logging failed: %v\n", err)
			} else {
				fmt.Println("\nLogged allocation run to database.")
			}
		}
	}
}

func exitWith(message string) {
	fmt.Fprintf(os.Stderr, "Error: %s\n", message)
	os.Exit(1)
}

func loadApplicants(path string) ([]*applicant, []string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to open CSV: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.TrimLeadingSpace = true

	header, err := reader.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("unable to read header: %w", err)
	}
	index := mapHeaders(header)

	required := []string{"applicant_id", "score", "need_level", "requested_amount"}
	missing := missingHeaders(required, index)
	if len(missing) > 0 {
		return nil, nil, fmt.Errorf("missing required headers: %s", strings.Join(missing, ", "))
	}

	var applicants []*applicant
	var warnings []string
	line := 1
	for {
		line++
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("line %d: %v", line, err))
			continue
		}
		item, warn := parseApplicant(record, index, line)
		if warn != "" {
			warnings = append(warnings, warn)
		}
		if item != nil {
			applicants = append(applicants, item)
		}
	}

	if len(applicants) == 0 {
		return nil, warnings, fmt.Errorf("no valid applicants found")
	}

	return applicants, warnings, nil
}

func mapHeaders(header []string) map[string]int {
	index := make(map[string]int, len(header))
	for i, name := range header {
		key := strings.ToLower(strings.TrimSpace(name))
		index[key] = i
	}
	return index
}

func missingHeaders(required []string, index map[string]int) []string {
	var missing []string
	for _, key := range required {
		if _, ok := index[key]; !ok {
			missing = append(missing, key)
		}
	}
	return missing
}

func parseApplicant(record []string, index map[string]int, line int) (*applicant, string) {
	get := func(key string) string {
		pos := index[key]
		if pos >= len(record) {
			return ""
		}
		return strings.TrimSpace(record[pos])
	}

	id := get("applicant_id")
	if id == "" {
		return nil, fmt.Sprintf("line %d: missing applicant_id", line)
	}
	name := ""
	if pos, ok := index["name"]; ok && pos < len(record) {
		name = strings.TrimSpace(record[pos])
	}

	score, err := strconv.ParseFloat(get("score"), 64)
	if err != nil {
		return nil, fmt.Sprintf("line %d: invalid score", line)
	}

	need := strings.ToLower(get("need_level"))
	requested, err := strconv.ParseFloat(get("requested_amount"), 64)
	if err != nil {
		return nil, fmt.Sprintf("line %d: invalid requested_amount", line)
	}

	applicant := &applicant{
		ID:        id,
		Name:      name,
		NeedLevel: need,
		ScoreRaw:  score,
		Requested: requested,
		Eligible:  true,
	}

	if requested <= 0 {
		markIneligible(applicant, "requested_amount must be > 0")
	}
	if need != "low" && need != "medium" && need != "high" {
		markIneligible(applicant, "need_level must be low, medium, or high")
	}

	return applicant, ""
}

func markIneligible(applicant *applicant, message string) {
	applicant.Eligible = false
	if applicant.EligibilityMsg == "" {
		applicant.EligibilityMsg = message
		return
	}
	applicant.EligibilityMsg = fmt.Sprintf("%s; %s", applicant.EligibilityMsg, message)
}

func applyMinScore(applicants []*applicant, minScore float64) {
	if minScore <= 0 {
		return
	}
	for _, item := range applicants {
		if item.ScoreRaw < minScore {
			markIneligible(item, fmt.Sprintf("score below minimum (%.1f)", minScore))
		}
	}
}

func normalizeScores(applicants []*applicant) {
	var maxScore float64
	for _, item := range applicants {
		if item.ScoreRaw > maxScore {
			maxScore = item.ScoreRaw
		}
	}
	if maxScore == 0 {
		maxScore = 1
	}
	for _, item := range applicants {
		item.ScoreNorm = item.ScoreRaw / maxScore
	}
}

func assignPriority(applicants []*applicant, scoreWeight, needWeight float64) {
	for _, item := range applicants {
		need := needWeight * needScore(item.NeedLevel)
		item.PriorityScore = (scoreWeight*item.ScoreNorm + need) / (scoreWeight + needWeight)
	}
}

func needScore(level string) float64 {
	switch strings.ToLower(level) {
	case "high":
		return 1
	case "medium":
		return 0.5
	case "low":
		return 0
	default:
		return 0
	}
}

func sortApplicants(applicants []*applicant) {
	sort.SliceStable(applicants, func(i, j int) bool {
		if applicants[i].PriorityScore == applicants[j].PriorityScore {
			return applicants[i].ScoreRaw > applicants[j].ScoreRaw
		}
		return applicants[i].PriorityScore > applicants[j].PriorityScore
	})
}

func allocateBudget(applicants []*applicant, budget, minAward, maxAward, reserveHigh, reserveMedium, reserveLow, roundTo, maxPercent float64) []*applicant {
	remaining := budget
	var awarded []*applicant

	reserves := []struct {
		level string
		share float64
	}{
		{level: "high", share: reserveHigh},
		{level: "medium", share: reserveMedium},
		{level: "low", share: reserveLow},
	}

	for _, reserve := range reserves {
		if reserve.share <= 0 {
			continue
		}
		reserved := budget * reserve.share
		if reserved <= 0 {
			continue
		}
		reservedAwards := allocatePass(applicants, reserved, minAward, maxAward, roundTo, maxPercent, func(item *applicant) bool {
			return item.NeedLevel == reserve.level && item.Awarded == 0
		})
		awarded = append(awarded, reservedAwards...)
		remaining -= totalAwarded(reservedAwards)
	}

	if remaining < 0 {
		remaining = 0
	}

	remainingAwards := allocatePass(applicants, remaining, minAward, maxAward, roundTo, maxPercent, func(item *applicant) bool {
		return item.Awarded == 0
	})
	awarded = append(awarded, remainingAwards...)
	return awarded
}

func allocatePass(applicants []*applicant, budget, minAward, maxAward, roundTo, maxPercent float64, allow func(*applicant) bool) []*applicant {
	remaining := budget
	var awarded []*applicant
	for _, item := range applicants {
		if !item.Eligible || !allow(item) {
			continue
		}
		award := computeAward(item.Requested, minAward, maxAward, roundTo, maxPercent)
		if award <= 0 {
			continue
		}
		if award > remaining {
			if remaining < minAward {
				break
			}
			award = remaining
		}
		item.Awarded = award
		remaining -= award
		awarded = append(awarded, item)
		if remaining <= 0 {
			break
		}
	}
	return awarded
}

func computeAward(requested, minAward, maxAward, roundTo, maxPercent float64) float64 {
	capAmount := maxAward
	percentCap := requested * maxPercent
	if percentCap < capAmount {
		capAmount = percentCap
	}
	if capAmount < 0 {
		capAmount = 0
	}
	award := clamp(requested, minAward, capAmount)
	if requested < minAward {
		award = requested
	}
	if award > capAmount {
		award = capAmount
	}
	if roundTo > 0 {
		award = roundToIncrement(award, roundTo)
		award = clamp(award, minAward, capAmount)
	}
	return award
}

func clamp(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func roundToIncrement(value, increment float64) float64 {
	if increment <= 0 {
		return value
	}
	rounded := float64(int(value/increment+0.5)) * increment
	return rounded
}

func summarize(applicants []*applicant, budget float64, awarded []*applicant) allocationSummary {
	byNeed := map[string]needAgg{
		"low":    {},
		"medium": {},
		"high":   {},
	}
	needCoverage := map[string]needCoverageAgg{
		"low":    {},
		"medium": {},
		"high":   {},
	}
	unfundedByNeed := map[string]needUnfundedAgg{
		"low":    {},
		"medium": {},
		"high":   {},
	}

	var budgetUsed float64
	var minAward float64
	var maxAward float64
	ineligibleReasons := make(map[string]int)
	var ineligibleCount int
	var eligibleCount int
	var unfundedCount int
	var unfundedAmount float64
	var eligibleRequestedTotal float64
	var fullyFundedCount int
	var partiallyFundedCount int
	var awardAmounts []float64
	var awardRates []float64
	var lastFundedPriority float64
	var lastFundedScore float64
	var lastFundedNeed string
	var lastFundedRequested float64
	if len(awarded) > 0 {
		minAward = awarded[0].Awarded
		maxAward = awarded[0].Awarded
		lastAward := awarded[len(awarded)-1]
		lastFundedPriority = lastAward.PriorityScore
		lastFundedScore = lastAward.ScoreRaw
		lastFundedNeed = lastAward.NeedLevel
		lastFundedRequested = lastAward.Requested
	}

	for _, item := range applicants {
		if !item.Eligible {
			ineligibleCount++
			if item.EligibilityMsg != "" {
				ineligibleReasons[item.EligibilityMsg]++
			}
			continue
		}
		eligibleCount++
		eligibleRequestedTotal += item.Requested
		coverage := needCoverage[item.NeedLevel]
		coverage.EligibleCount++
		coverage.RequestedTotal += item.Requested
		if item.Awarded > 0 {
			coverage.AwardedCount++
			coverage.AwardedTotal += item.Awarded
		}
		if item.Awarded == 0 {
			unfundedCount++
			unfundedAmount += item.Requested
			agg := unfundedByNeed[item.NeedLevel]
			agg.Count++
			agg.Requested += item.Requested
			unfundedByNeed[item.NeedLevel] = agg
			coverage.UnfundedCount++
			needCoverage[item.NeedLevel] = coverage
			continue
		}
		if item.Awarded >= item.Requested {
			fullyFundedCount++
		} else {
			partiallyFundedCount++
		}
		needCoverage[item.NeedLevel] = coverage
	}

	for _, item := range awarded {
		budgetUsed += item.Awarded
		awardAmounts = append(awardAmounts, item.Awarded)
		if item.Requested > 0 {
			awardRates = append(awardRates, item.Awarded/item.Requested)
		}
		if item.Awarded < minAward {
			minAward = item.Awarded
		}
		if item.Awarded > maxAward {
			maxAward = item.Awarded
		}

		agg := byNeed[item.NeedLevel]
		agg.AwardedCount++
		agg.BudgetUsed += item.Awarded
		byNeed[item.NeedLevel] = agg
	}

	for level, coverage := range needCoverage {
		if coverage.RequestedTotal > 0 {
			coverage.CoverageRate = coverage.AwardedTotal / coverage.RequestedTotal
		}
		needCoverage[level] = coverage
	}

	for level, coverage := range needCoverage {
		if eligibleRequestedTotal > 0 {
			coverage.RequestedShare = coverage.RequestedTotal / eligibleRequestedTotal
		}
		if budgetUsed > 0 {
			coverage.AwardedShare = coverage.AwardedTotal / budgetUsed
		}
		coverage.ShareDelta = coverage.AwardedShare - coverage.RequestedShare
		needCoverage[level] = coverage
	}

	averageAward := 0.0
	if len(awarded) > 0 {
		averageAward = budgetUsed / float64(len(awarded))
	}
	coverageRate := 0.0
	if eligibleRequestedTotal > 0 {
		coverageRate = budgetUsed / eligibleRequestedTotal
	}
	fundingGapTotal := eligibleRequestedTotal - budgetUsed
	if fundingGapTotal < 0 {
		fundingGapTotal = 0
	}
	budgetShortfall := eligibleRequestedTotal - budget
	if budgetShortfall < 0 {
		budgetShortfall = 0
	}
	fullFundingRate := 0.0
	if eligibleCount > 0 {
		fullFundingRate = float64(fullyFundedCount) / float64(eligibleCount)
	}
	awardP25 := percentile(awardAmounts, 0.25)
	awardP50 := percentile(awardAmounts, 0.50)
	awardP75 := percentile(awardAmounts, 0.75)
	awardToRequestAvg := averageFloat(awardRates)

	return allocationSummary{
		GeneratedAt:             time.Now().Format(time.RFC3339),
		Budget:                  budget,
		BudgetUsed:              budgetUsed,
		BudgetLeft:              budget - budgetUsed,
		BudgetRequiredFull:      eligibleRequestedTotal,
		BudgetShortfall:         budgetShortfall,
		Applicants:              len(applicants),
		EligibleCount:           eligibleCount,
		AwardedCount:            len(awarded),
		IneligibleCount:         ineligibleCount,
		EligibleUnfundedCount:   unfundedCount,
		EligibleUnfundedAmount:  unfundedAmount,
		EligibleRequestedTotal:  eligibleRequestedTotal,
		FullyFundedCount:        fullyFundedCount,
		PartiallyFundedCount:    partiallyFundedCount,
		FundingGapTotal:         fundingGapTotal,
		CoverageRate:            coverageRate,
		FullFundingRate:         fullFundingRate,
		AverageAward:            averageAward,
		AwardP25:                awardP25,
		AwardP50:                awardP50,
		AwardP75:                awardP75,
		AwardToRequestAvg:       awardToRequestAvg,
		MinAwarded:              minAward,
		MaxAwarded:              maxAward,
		LastFundedPriority:      lastFundedPriority,
		LastFundedScore:         lastFundedScore,
		LastFundedNeed:          lastFundedNeed,
		LastFundedRequested:     lastFundedRequested,
		ByNeed:                  byNeed,
		NeedCoverage:            needCoverage,
		UnfundedByNeed:          unfundedByNeed,
		IneligibleReasonSummary: ineligibleReasons,
		Awards:                  buildAwardRecords(awarded),
		Unfunded:                buildUnfundedRecords(applicants),
		Ineligible:              buildIneligibleRecords(applicants),
	}
}

func parseBudgetList(raw string) ([]float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	var budgets []float64
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid scenario budget: %s", value)
		}
		if parsed <= 0 {
			return nil, fmt.Errorf("scenario budgets must be > 0")
		}
		budgets = append(budgets, parsed)
	}
	return budgets, nil
}

func buildScenarioResults(applicants []*applicant, budgets []float64, minAward, maxAward, reserveHigh, reserveMedium, reserveLow, roundTo, maxPercent float64) []scenarioResult {
	results := make([]scenarioResult, 0, len(budgets))
	for _, budget := range budgets {
		clone := cloneApplicants(applicants)
		awarded := allocateBudget(clone, budget, minAward, maxAward, reserveHigh, reserveMedium, reserveLow, roundTo, maxPercent)
		results = append(results, summarizeScenario(clone, awarded, budget))
	}
	return results
}

func cloneApplicants(applicants []*applicant) []*applicant {
	clone := make([]*applicant, 0, len(applicants))
	for _, item := range applicants {
		copyItem := *item
		copyItem.Awarded = 0
		clone = append(clone, &copyItem)
	}
	return clone
}

func summarizeScenario(applicants []*applicant, awarded []*applicant, budget float64) scenarioResult {
	var eligibleCount int
	var unfundedCount int
	var fullyFundedCount int
	var partiallyFundedCount int
	var eligibleRequestedTotal float64
	var awardRates []float64
	for _, item := range applicants {
		if !item.Eligible {
			continue
		}
		eligibleCount++
		eligibleRequestedTotal += item.Requested
		if item.Awarded == 0 {
			unfundedCount++
			continue
		}
		if item.Awarded >= item.Requested {
			fullyFundedCount++
		} else {
			partiallyFundedCount++
		}
		if item.Requested > 0 {
			awardRates = append(awardRates, item.Awarded/item.Requested)
		}
	}

	budgetUsed := totalAwarded(awarded)
	averageAward := 0.0
	if len(awarded) > 0 {
		averageAward = budgetUsed / float64(len(awarded))
	}
	coverageRate := 0.0
	if eligibleRequestedTotal > 0 {
		coverageRate = budgetUsed / eligibleRequestedTotal
	}
	fundingGapTotal := eligibleRequestedTotal - budgetUsed
	if fundingGapTotal < 0 {
		fundingGapTotal = 0
	}
	fullFundingRate := 0.0
	if eligibleCount > 0 {
		fullFundingRate = float64(fullyFundedCount) / float64(eligibleCount)
	}

	return scenarioResult{
		Budget:                budget,
		BudgetUsed:            budgetUsed,
		BudgetLeft:            budget - budgetUsed,
		BudgetRequiredFull:    eligibleRequestedTotal,
		AwardedCount:          len(awarded),
		EligibleCount:         eligibleCount,
		EligibleUnfundedCount: unfundedCount,
		FullyFundedCount:      fullyFundedCount,
		PartiallyFundedCount:  partiallyFundedCount,
		CoverageRate:          coverageRate,
		FullFundingRate:       fullFundingRate,
		FundingGapTotal:       fundingGapTotal,
		AverageAward:          averageAward,
		AwardToRequestAvg:     averageFloat(awardRates),
	}
}

func buildAwardRecords(awarded []*applicant) []awardRecord {
	records := make([]awardRecord, 0, len(awarded))
	for _, item := range awarded {
		records = append(records, awardRecord{
			ApplicantID: item.ID,
			Name:        item.Name,
			NeedLevel:   item.NeedLevel,
			Score:       item.ScoreRaw,
			Requested:   item.Requested,
			Awarded:     item.Awarded,
			Priority:    item.PriorityScore,
		})
	}
	return records
}

func buildUnfundedRecords(applicants []*applicant) []awardRecord {
	var records []awardRecord
	for _, item := range applicants {
		if !item.Eligible || item.Awarded > 0 {
			continue
		}
		records = append(records, awardRecord{
			ApplicantID: item.ID,
			Name:        item.Name,
			NeedLevel:   item.NeedLevel,
			Score:       item.ScoreRaw,
			Requested:   item.Requested,
			Awarded:     item.Awarded,
			Priority:    item.PriorityScore,
		})
	}
	return records
}

func buildIneligibleRecords(applicants []*applicant) []ineligibleRecord {
	var records []ineligibleRecord
	for _, item := range applicants {
		if item.Eligible {
			continue
		}
		records = append(records, ineligibleRecord{
			ApplicantID: item.ID,
			Name:        item.Name,
			NeedLevel:   item.NeedLevel,
			Score:       item.ScoreRaw,
			Requested:   item.Requested,
			Reason:      item.EligibilityMsg,
		})
	}
	return records
}

func printSummary(summary allocationSummary) {
	fmt.Println("Award Allocation Summary")
	fmt.Println(strings.Repeat("-", 26))
	fmt.Printf("Applicants:   %d\n", summary.Applicants)
	fmt.Printf("Eligible:     %d\n", summary.EligibleCount)
	fmt.Printf("Awarded:      %d\n", summary.AwardedCount)
	fmt.Printf("Ineligible:   %d\n", summary.IneligibleCount)
	fmt.Printf("Eligible Unfunded: %d ($%.2f requested)\n", summary.EligibleUnfundedCount, summary.EligibleUnfundedAmount)
	fmt.Printf("Eligible Requested: $%.2f\n", summary.EligibleRequestedTotal)
	fmt.Printf("Budget Required (Full Funding): $%.2f\n", summary.BudgetRequiredFull)
	fmt.Printf("Budget Shortfall: $%.2f\n", summary.BudgetShortfall)
	fmt.Printf("Coverage Rate: %.1f%%\n", summary.CoverageRate*100)
	fmt.Printf("Fully Funded: %d (%.1f%% of eligible)\n", summary.FullyFundedCount, summary.FullFundingRate*100)
	fmt.Printf("Partially Funded: %d\n", summary.PartiallyFundedCount)
	fmt.Printf("Funding Gap:  $%.2f\n", summary.FundingGapTotal)
	fmt.Printf("Budget Used:  $%.2f\n", summary.BudgetUsed)
	fmt.Printf("Budget Left:  $%.2f\n", summary.BudgetLeft)
	fmt.Printf("Average Award $%.2f\n", summary.AverageAward)
	fmt.Printf("Award Percentiles: P25 $%.2f | P50 $%.2f | P75 $%.2f\n", summary.AwardP25, summary.AwardP50, summary.AwardP75)
	fmt.Printf("Avg Award/Request: %.1f%%\n", summary.AwardToRequestAvg*100)
	fmt.Printf("Award Range:  $%.2f - $%.2f\n", summary.MinAwarded, summary.MaxAwarded)
	if summary.AwardedCount > 0 {
		fmt.Printf("Last Funded Cutoff: %.2f priority | %.1f score | %s need | $%.2f requested\n",
			summary.LastFundedPriority,
			summary.LastFundedScore,
			strings.Title(summary.LastFundedNeed),
			summary.LastFundedRequested,
		)
	}
	printIneligibleReasons(summary.IneligibleReasonSummary)
	fmt.Println("\nBy Need Level")
	fmt.Println(strings.Repeat("-", 13))
	needKeys := []string{"high", "medium", "low"}
	for _, level := range needKeys {
		agg := summary.ByNeed[level]
		fmt.Printf("%s: %d awarded ($%.2f)\n", strings.Title(level), agg.AwardedCount, agg.BudgetUsed)
	}
	printNeedCoverage(summary.NeedCoverage)
	printNeedEquity(summary.NeedCoverage)
	printUnfundedByNeed(summary.UnfundedByNeed)
}

func printScenarioResults(results []scenarioResult) {
	if len(results) == 0 {
		return
	}
	fmt.Println("\nScenario Analysis")
	fmt.Println(strings.Repeat("-", 16))
	fmt.Printf("%-12s | %-7s | %-8s | %-9s | %-11s | %-11s | %-11s\n",
		"Budget", "Awarded", "Unfunded", "Coverage", "Full Funded", "Budget Used", "Budget Left")
	for _, result := range results {
		fmt.Printf("%-12s | %-7d | %-8d | %-9s | %-11s | %-11s | %-11s\n",
			formatCurrency(result.Budget),
			result.AwardedCount,
			result.EligibleUnfundedCount,
			formatPercent(result.CoverageRate),
			formatPercent(result.FullFundingRate),
			formatCurrency(result.BudgetUsed),
			formatCurrency(result.BudgetLeft),
		)
	}
}

func printNeedCoverage(coverage map[string]needCoverageAgg) {
	if len(coverage) == 0 {
		return
	}
	fmt.Println("\nNeed Coverage")
	fmt.Println(strings.Repeat("-", 13))
	needKeys := []string{"high", "medium", "low"}
	for _, level := range needKeys {
		agg := coverage[level]
		fmt.Printf("%s: %d eligible | %d awarded | %d unfunded | $%.2f requested | $%.2f awarded | %.1f%% coverage\n",
			strings.Title(level),
			agg.EligibleCount,
			agg.AwardedCount,
			agg.UnfundedCount,
			agg.RequestedTotal,
			agg.AwardedTotal,
			agg.CoverageRate*100,
		)
	}
}

func printNeedEquity(coverage map[string]needCoverageAgg) {
	if len(coverage) == 0 {
		return
	}
	fmt.Println("\nNeed Equity (Requested vs Awarded Share)")
	fmt.Println(strings.Repeat("-", 38))
	needKeys := []string{"high", "medium", "low"}
	for _, level := range needKeys {
		agg := coverage[level]
		fmt.Printf("%s: %.1f%% requested | %.1f%% awarded | %+0.1f%% delta\n",
			strings.Title(level),
			agg.RequestedShare*100,
			agg.AwardedShare*100,
			agg.ShareDelta*100,
		)
	}
}

func printIneligibleReasons(reasons map[string]int) {
	if len(reasons) == 0 {
		return
	}
	type reasonCount struct {
		Reason string
		Count  int
	}
	var list []reasonCount
	for reason, count := range reasons {
		list = append(list, reasonCount{Reason: reason, Count: count})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Count == list[j].Count {
			return list[i].Reason < list[j].Reason
		}
		return list[i].Count > list[j].Count
	})
	fmt.Println("\nIneligible Reasons")
	fmt.Println(strings.Repeat("-", 18))
	limit := len(list)
	if limit > 3 {
		limit = 3
	}
	for i := 0; i < limit; i++ {
		fmt.Printf("%s: %d\n", list[i].Reason, list[i].Count)
	}
	if limit < len(list) {
		fmt.Printf("... %d more\n", len(list)-limit)
	}
}

func totalAwarded(awarded []*applicant) float64 {
	var total float64
	for _, item := range awarded {
		total += item.Awarded
	}
	return total
}

func printAwards(awarded []*applicant, topN int, showAll bool) {
	if len(awarded) == 0 {
		fmt.Println("\nNo awards allocated.")
		return
	}
	fmt.Println("\nAwarded Applicants")
	fmt.Println(strings.Repeat("-", 19))
	limit := len(awarded)
	if !showAll && topN > 0 && topN < limit {
		limit = topN
	}
	for i := 0; i < limit; i++ {
		item := awarded[i]
		label := item.ID
		if item.Name != "" {
			label = fmt.Sprintf("%s (%s)", item.Name, item.ID)
		}
		fmt.Printf("%d. %s | Need: %s | Score: %.1f | Requested: $%.2f | Awarded: $%.2f | Priority: %.2f\n",
			i+1, label, strings.Title(item.NeedLevel), item.ScoreRaw, item.Requested, item.Awarded, item.PriorityScore)
	}
	if limit < len(awarded) {
		fmt.Printf("... %d more\n", len(awarded)-limit)
	}
}

func printUnfunded(unfunded []awardRecord, topN int, showAll bool) {
	if len(unfunded) == 0 {
		fmt.Println("\nNo eligible unfunded applicants.")
		return
	}
	fmt.Println("\nUnfunded Eligible Applicants")
	fmt.Println(strings.Repeat("-", 28))
	limit := len(unfunded)
	if !showAll && topN > 0 && topN < limit {
		limit = topN
	}
	for i := 0; i < limit; i++ {
		item := unfunded[i]
		label := item.ApplicantID
		if item.Name != "" {
			label = fmt.Sprintf("%s (%s)", item.Name, item.ApplicantID)
		}
		fmt.Printf("%d. %s | Need: %s | Score: %.1f | Requested: $%.2f | Priority: %.2f\n",
			i+1, label, strings.Title(item.NeedLevel), item.Score, item.Requested, item.Priority)
	}
	if limit < len(unfunded) {
		fmt.Printf("... %d more\n", len(unfunded)-limit)
	}
}

func printUnfundedByNeed(byNeed map[string]needUnfundedAgg) {
	if len(byNeed) == 0 {
		return
	}
	fmt.Println("\nUnfunded By Need Level")
	fmt.Println(strings.Repeat("-", 23))
	needKeys := []string{"high", "medium", "low"}
	for _, level := range needKeys {
		agg := byNeed[level]
		fmt.Printf("%s: %d unfunded ($%.2f requested)\n", strings.Title(level), agg.Count, agg.Requested)
	}
}

func writeJSON(path string, summary allocationSummary, awarded []*applicant) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("unable to create JSON output: %w", err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(summary); err != nil {
		return fmt.Errorf("unable to write JSON output: %w", err)
	}
	return nil
}

func writeAwardsCSV(path string, awarded []*applicant) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("unable to create awards CSV: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	if err := writer.Write([]string{"applicant_id", "name", "need_level", "score", "requested_amount", "awarded_amount", "priority"}); err != nil {
		return fmt.Errorf("write awards CSV header: %w", err)
	}
	for _, item := range awarded {
		row := []string{
			item.ID,
			item.Name,
			item.NeedLevel,
			formatFloat(item.ScoreRaw, 1),
			formatFloat(item.Requested, 2),
			formatFloat(item.Awarded, 2),
			formatFloat(item.PriorityScore, 4),
		}
		if err := writer.Write(row); err != nil {
			return fmt.Errorf("write awards CSV row: %w", err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return fmt.Errorf("flush awards CSV: %w", err)
	}
	return nil
}

func writeUnfundedCSV(path string, unfunded []awardRecord) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("unable to create unfunded CSV: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	if err := writer.Write([]string{"applicant_id", "name", "need_level", "score", "requested_amount", "priority"}); err != nil {
		return fmt.Errorf("write unfunded CSV header: %w", err)
	}
	for _, item := range unfunded {
		row := []string{
			item.ApplicantID,
			item.Name,
			item.NeedLevel,
			formatFloat(item.Score, 1),
			formatFloat(item.Requested, 2),
			formatFloat(item.Priority, 4),
		}
		if err := writer.Write(row); err != nil {
			return fmt.Errorf("write unfunded CSV row: %w", err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return fmt.Errorf("flush unfunded CSV: %w", err)
	}
	return nil
}

func writeIneligibleCSV(path string, ineligible []ineligibleRecord) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("unable to create ineligible CSV: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	if err := writer.Write([]string{"applicant_id", "name", "need_level", "score", "requested_amount", "eligibility_reason"}); err != nil {
		return fmt.Errorf("write ineligible CSV header: %w", err)
	}
	for _, item := range ineligible {
		row := []string{
			item.ApplicantID,
			item.Name,
			item.NeedLevel,
			formatFloat(item.Score, 1),
			formatFloat(item.Requested, 2),
			item.Reason,
		}
		if err := writer.Write(row); err != nil {
			return fmt.Errorf("write ineligible CSV row: %w", err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return fmt.Errorf("flush ineligible CSV: %w", err)
	}
	return nil
}

func writeReport(path string, summary allocationSummary, topN int, showAll bool, unfundedTop int, showAllUnfunded bool) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("unable to create report: %w", err)
	}
	defer file.Close()

	fmt.Fprintln(file, "# Award Allocation Report")
	fmt.Fprintf(file, "\nGenerated: %s\n", summary.GeneratedAt)

	fmt.Fprintln(file, "\n## Budget")
	fmt.Fprintf(file, "- Budget: %s\n", formatCurrency(summary.Budget))
	fmt.Fprintf(file, "- Budget used: %s\n", formatCurrency(summary.BudgetUsed))
	fmt.Fprintf(file, "- Budget left: %s\n", formatCurrency(summary.BudgetLeft))

	fmt.Fprintln(file, "\n## Eligibility")
	fmt.Fprintf(file, "- Applicants: %d\n", summary.Applicants)
	fmt.Fprintf(file, "- Eligible: %d\n", summary.EligibleCount)
	fmt.Fprintf(file, "- Awarded: %d\n", summary.AwardedCount)
	fmt.Fprintf(file, "- Ineligible: %d\n", summary.IneligibleCount)
	fmt.Fprintf(file, "- Eligible unfunded: %d (%s requested)\n", summary.EligibleUnfundedCount, formatCurrency(summary.EligibleUnfundedAmount))
	fmt.Fprintf(file, "- Eligible requested: %s\n", formatCurrency(summary.EligibleRequestedTotal))
	fmt.Fprintf(file, "- Coverage rate: %s\n", formatPercent(summary.CoverageRate))
	fmt.Fprintf(file, "- Fully funded: %d (%s of eligible)\n", summary.FullyFundedCount, formatPercent(summary.FullFundingRate))
	fmt.Fprintf(file, "- Partially funded: %d\n", summary.PartiallyFundedCount)
	fmt.Fprintf(file, "- Funding gap: %s\n", formatCurrency(summary.FundingGapTotal))
	fmt.Fprintf(file, "- Average award: %s\n", formatCurrency(summary.AverageAward))
	fmt.Fprintf(file, "- Award percentiles: P25 %s | P50 %s | P75 %s\n", formatCurrency(summary.AwardP25), formatCurrency(summary.AwardP50), formatCurrency(summary.AwardP75))
	fmt.Fprintf(file, "- Avg award/request: %s\n", formatPercent(summary.AwardToRequestAvg))
	fmt.Fprintf(file, "- Award range: %s - %s\n", formatCurrency(summary.MinAwarded), formatCurrency(summary.MaxAwarded))

	if summary.AwardedCount > 0 {
		fmt.Fprintf(file, "- Last funded cutoff: %.2f priority | %.1f score | %s need | %s requested\n",
			summary.LastFundedPriority,
			summary.LastFundedScore,
			strings.Title(summary.LastFundedNeed),
			formatCurrency(summary.LastFundedRequested),
		)
	}

	fmt.Fprintln(file, "\n## Awards")
	awardRows := limitAwardRecords(summary.Awards, topN, showAll)
	if len(awardRows) == 0 {
		fmt.Fprintln(file, "_No awards allocated._")
	} else {
		fmt.Fprintln(file, "| Rank | Applicant | Need | Score | Requested | Awarded | Priority |")
		fmt.Fprintln(file, "| --- | --- | --- | --- | --- | --- | --- |")
		for i, item := range awardRows {
			fmt.Fprintf(file, "| %d | %s | %s | %.1f | %s | %s | %.2f |\n",
				i+1,
				formatApplicantLabel(item.ApplicantID, item.Name),
				strings.Title(item.NeedLevel),
				item.Score,
				formatCurrency(item.Requested),
				formatCurrency(item.Awarded),
				item.Priority,
			)
		}
		if !showAll && topN > 0 && len(summary.Awards) > len(awardRows) {
			fmt.Fprintf(file, "\n_Showing top %d of %d awards._\n", len(awardRows), len(summary.Awards))
		}
	}

	fmt.Fprintln(file, "\n## Unfunded Eligible Applicants")
	unfundedRows := limitAwardRecords(summary.Unfunded, unfundedTop, showAllUnfunded)
	if len(unfundedRows) == 0 {
		fmt.Fprintln(file, "_No eligible unfunded applicants._")
	} else {
		fmt.Fprintln(file, "| Rank | Applicant | Need | Score | Requested | Priority |")
		fmt.Fprintln(file, "| --- | --- | --- | --- | --- | --- |")
		for i, item := range unfundedRows {
			fmt.Fprintf(file, "| %d | %s | %s | %.1f | %s | %.2f |\n",
				i+1,
				formatApplicantLabel(item.ApplicantID, item.Name),
				strings.Title(item.NeedLevel),
				item.Score,
				formatCurrency(item.Requested),
				item.Priority,
			)
		}
		if !showAllUnfunded && unfundedTop > 0 && len(summary.Unfunded) > len(unfundedRows) {
			fmt.Fprintf(file, "\n_Showing top %d of %d unfunded applicants._\n", len(unfundedRows), len(summary.Unfunded))
		}
	}

	fmt.Fprintln(file, "\n## Need Coverage")
	fmt.Fprintln(file, "| Need Level | Eligible | Awarded | Unfunded | Requested | Awarded Total | Coverage |")
	fmt.Fprintln(file, "| --- | --- | --- | --- | --- | --- | --- |")
	needKeys := []string{"high", "medium", "low"}
	for _, level := range needKeys {
		agg := summary.NeedCoverage[level]
		fmt.Fprintf(file, "| %s | %d | %d | %d | %s | %s | %s |\n",
			strings.Title(level),
			agg.EligibleCount,
			agg.AwardedCount,
			agg.UnfundedCount,
			formatCurrency(agg.RequestedTotal),
			formatCurrency(agg.AwardedTotal),
			formatPercent(agg.CoverageRate),
		)
	}

	if len(summary.ScenarioResults) > 0 {
		fmt.Fprintln(file, "\n## Scenario Analysis")
		fmt.Fprintln(file, "| Budget | Awarded | Unfunded | Coverage | Full Funding | Budget Used | Budget Left |")
		fmt.Fprintln(file, "| --- | --- | --- | --- | --- | --- | --- |")
		for _, result := range summary.ScenarioResults {
			fmt.Fprintf(file, "| %s | %d | %d | %s | %s | %s | %s |\n",
				formatCurrency(result.Budget),
				result.AwardedCount,
				result.EligibleUnfundedCount,
				formatPercent(result.CoverageRate),
				formatPercent(result.FullFundingRate),
				formatCurrency(result.BudgetUsed),
				formatCurrency(result.BudgetLeft),
			)
		}
	}

	if len(summary.IneligibleReasonSummary) > 0 {
		fmt.Fprintln(file, "\n## Ineligible Reasons")
		reasonRows := sortReasonSummary(summary.IneligibleReasonSummary)
		for _, item := range reasonRows {
			fmt.Fprintf(file, "- %s: %d\n", item.Reason, item.Count)
		}
	}

	return nil
}

func formatFloat(value float64, decimals int) string {
	return strconv.FormatFloat(value, 'f', decimals, 64)
}

func formatCurrency(value float64) string {
	return fmt.Sprintf("$%.2f", value)
}

func formatPercent(value float64) string {
	return fmt.Sprintf("%.1f%%", value*100)
}

func formatApplicantLabel(id, name string) string {
	if name == "" {
		return id
	}
	return fmt.Sprintf("%s (%s)", name, id)
}

func limitAwardRecords(records []awardRecord, limit int, showAll bool) []awardRecord {
	if showAll || limit <= 0 || limit >= len(records) {
		return records
	}
	return records[:limit]
}

type reasonSummary struct {
	Reason string
	Count  int
}

func sortReasonSummary(reasons map[string]int) []reasonSummary {
	list := make([]reasonSummary, 0, len(reasons))
	for reason, count := range reasons {
		list = append(list, reasonSummary{Reason: reason, Count: count})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Count == list[j].Count {
			return list[i].Reason < list[j].Reason
		}
		return list[i].Count > list[j].Count
	})
	return list
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if p <= 0 {
		return minFloat(values)
	}
	if p >= 1 {
		return maxFloat(values)
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	rank := int(math.Ceil(p*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

func averageFloat(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, value := range values {
		sum += value
	}
	return sum / float64(len(values))
}

func minFloat(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	min := values[0]
	for _, value := range values[1:] {
		if value < min {
			min = value
		}
	}
	return min
}

func maxFloat(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	max := values[0]
	for _, value := range values[1:] {
		if value > max {
			max = value
		}
	}
	return max
}

type dbConfig struct {
	Enabled bool
	URL     string
	Schema  string
}

type dbRunOptions struct {
	MinAward      float64
	MaxAward      float64
	ScoreWeight   float64
	NeedWeight    float64
	ReserveHigh   float64
	ReserveMedium float64
	ReserveLow    float64
	RoundTo       float64
	MaxPercent    float64
	MinScore      float64
}

func loadDBConfig() (dbConfig, error) {
	url := strings.TrimSpace(os.Getenv("GS_AWARD_ALLOCATOR_DB_URL"))
	if url == "" {
		return dbConfig{Enabled: false}, nil
	}
	schema := strings.TrimSpace(os.Getenv("GS_AWARD_ALLOCATOR_SCHEMA"))
	if schema == "" {
		schema = "gs_award_allocator"
	}
	schema, err := sanitizeIdentifier(schema)
	if err != nil {
		return dbConfig{}, err
	}
	return dbConfig{
		Enabled: true,
		URL:     url,
		Schema:  schema,
	}, nil
}

func sanitizeIdentifier(value string) (string, error) {
	if value == "" {
		return "", errors.New("schema must not be empty")
	}
	value = strings.ToLower(value)
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || r == '_' || (r >= '0' && r <= '9' && i > 0) {
			continue
		}
		return "", fmt.Errorf("invalid schema name: %s", value)
	}
	return value, nil
}

func logRunToDatabase(ctx context.Context, cfg dbConfig, summary allocationSummary, applicants []*applicant, inputPath string, opts dbRunOptions) error {
	pool, err := pgxpool.New(ctx, cfg.URL)
	if err != nil {
		return fmt.Errorf("open pool: %w", err)
	}
	defer pool.Close()

	if err := ensureDBSchema(ctx, pool, cfg.Schema); err != nil {
		return err
	}

	runID := uuid.New()
	if err := insertRun(ctx, pool, cfg.Schema, runID, summary, inputPath, opts); err != nil {
		return err
	}
	if err := insertApplicants(ctx, pool, cfg.Schema, runID, applicants); err != nil {
		return err
	}
	if err := insertNeedCoverage(ctx, pool, cfg.Schema, runID, summary.NeedCoverage); err != nil {
		return err
	}
	return nil
}

func ensureDBSchema(ctx context.Context, pool *pgxpool.Pool, schema string) error {
	_, err := pool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schema))
	if err != nil {
		return fmt.Errorf("create schema: %w", err)
	}

	runTable := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s.runs (
  run_id uuid PRIMARY KEY,
  generated_at timestamptz NOT NULL,
  input_path text,
  budget numeric NOT NULL,
  budget_used numeric NOT NULL,
  budget_left numeric NOT NULL,
  budget_required_full numeric NOT NULL,
  budget_shortfall numeric NOT NULL,
  applicants int NOT NULL,
  eligible_count int NOT NULL,
  awarded_count int NOT NULL,
  ineligible_count int NOT NULL,
  eligible_unfunded_count int NOT NULL,
  eligible_unfunded_amount numeric NOT NULL,
  eligible_requested_total numeric NOT NULL,
  fully_funded_count int NOT NULL,
  partially_funded_count int NOT NULL,
  funding_gap_total numeric NOT NULL,
  coverage_rate numeric NOT NULL,
  full_funding_rate numeric NOT NULL,
  average_award numeric NOT NULL,
  award_p25 numeric NOT NULL,
  award_p50 numeric NOT NULL,
  award_p75 numeric NOT NULL,
  award_to_request_avg numeric NOT NULL,
  min_awarded numeric NOT NULL,
  max_awarded numeric NOT NULL,
  last_funded_priority numeric NOT NULL,
  last_funded_score numeric NOT NULL,
  last_funded_need text NOT NULL,
  last_funded_requested numeric NOT NULL,
  min_award_option numeric NOT NULL,
  max_award_option numeric NOT NULL,
  score_weight numeric NOT NULL,
  need_weight numeric NOT NULL,
  reserve_high numeric NOT NULL,
  reserve_medium numeric NOT NULL,
  reserve_low numeric NOT NULL,
  round_to numeric NOT NULL,
  max_percent numeric NOT NULL,
  min_score numeric NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);`, schema)
	if _, err := pool.Exec(ctx, runTable); err != nil {
		return fmt.Errorf("create runs table: %w", err)
	}
	if err := ensureRunColumns(ctx, pool, schema); err != nil {
		return err
	}

	applicantTable := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s.applicants (
  id bigserial PRIMARY KEY,
  run_id uuid NOT NULL REFERENCES %s.runs(run_id) ON DELETE CASCADE,
  applicant_id text NOT NULL,
  name text,
  need_level text,
  score_raw numeric,
  score_norm numeric,
  priority numeric,
  requested numeric,
  awarded numeric,
  eligible boolean,
  eligibility_msg text
);`, schema, schema)
	if _, err := pool.Exec(ctx, applicantTable); err != nil {
		return fmt.Errorf("create applicants table: %w", err)
	}

	indexSQL := fmt.Sprintf("CREATE INDEX IF NOT EXISTS applicants_run_id_idx ON %s.applicants(run_id);", schema)
	if _, err := pool.Exec(ctx, indexSQL); err != nil {
		return fmt.Errorf("create index: %w", err)
	}

	needCoverageTable := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s.need_coverage (
  id bigserial PRIMARY KEY,
  run_id uuid NOT NULL REFERENCES %s.runs(run_id) ON DELETE CASCADE,
  need_level text NOT NULL,
  eligible_count int NOT NULL,
  awarded_count int NOT NULL,
  unfunded_count int NOT NULL,
  requested_total numeric NOT NULL,
  awarded_total numeric NOT NULL,
  coverage_rate numeric NOT NULL,
  requested_share numeric NOT NULL,
  awarded_share numeric NOT NULL,
  share_delta numeric NOT NULL
);`, schema, schema)
	if _, err := pool.Exec(ctx, needCoverageTable); err != nil {
		return fmt.Errorf("create need_coverage table: %w", err)
	}

	if err := ensureNeedCoverageColumns(ctx, pool, schema); err != nil {
		return err
	}

	coverageIndex := fmt.Sprintf("CREATE INDEX IF NOT EXISTS need_coverage_run_id_idx ON %s.need_coverage(run_id);", schema)
	if _, err := pool.Exec(ctx, coverageIndex); err != nil {
		return fmt.Errorf("create need_coverage index: %w", err)
	}
	return nil
}

func ensureRunColumns(ctx context.Context, pool *pgxpool.Pool, schema string) error {
	alter := fmt.Sprintf(`
ALTER TABLE %s.runs
  ADD COLUMN IF NOT EXISTS eligible_count int NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS fully_funded_count int NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS partially_funded_count int NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS funding_gap_total numeric NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS full_funding_rate numeric NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS award_p25 numeric NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS award_p50 numeric NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS award_p75 numeric NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS award_to_request_avg numeric NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS last_funded_priority numeric NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS last_funded_score numeric NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS last_funded_need text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS last_funded_requested numeric NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS budget_required_full numeric NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS budget_shortfall numeric NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS reserve_medium numeric NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS reserve_low numeric NOT NULL DEFAULT 0;`, schema)
	if _, err := pool.Exec(ctx, alter); err != nil {
		return fmt.Errorf("alter runs table: %w", err)
	}
	return nil
}

func ensureNeedCoverageColumns(ctx context.Context, pool *pgxpool.Pool, schema string) error {
	alter := fmt.Sprintf(`
ALTER TABLE %s.need_coverage
  ADD COLUMN IF NOT EXISTS requested_share numeric NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS awarded_share numeric NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS share_delta numeric NOT NULL DEFAULT 0;`, schema)
	if _, err := pool.Exec(ctx, alter); err != nil {
		return fmt.Errorf("alter need_coverage table: %w", err)
	}
	return nil
}

func insertRun(ctx context.Context, pool *pgxpool.Pool, schema string, runID uuid.UUID, summary allocationSummary, inputPath string, opts dbRunOptions) error {
	builder := sq.Insert(schema+".runs").
		Columns(
			"run_id",
			"generated_at",
			"input_path",
			"budget",
			"budget_used",
			"budget_left",
			"budget_required_full",
			"budget_shortfall",
			"applicants",
			"eligible_count",
			"awarded_count",
			"ineligible_count",
			"eligible_unfunded_count",
			"eligible_unfunded_amount",
			"eligible_requested_total",
			"fully_funded_count",
			"partially_funded_count",
			"funding_gap_total",
			"coverage_rate",
			"full_funding_rate",
			"average_award",
			"award_p25",
			"award_p50",
			"award_p75",
			"award_to_request_avg",
			"min_awarded",
			"max_awarded",
			"last_funded_priority",
			"last_funded_score",
			"last_funded_need",
			"last_funded_requested",
			"min_award_option",
			"max_award_option",
			"score_weight",
			"need_weight",
			"reserve_high",
			"reserve_medium",
			"reserve_low",
			"round_to",
			"max_percent",
			"min_score",
		).
		Values(
			runID,
			summary.GeneratedAt,
			inputPath,
			summary.Budget,
			summary.BudgetUsed,
			summary.BudgetLeft,
			summary.BudgetRequiredFull,
			summary.BudgetShortfall,
			summary.Applicants,
			summary.EligibleCount,
			summary.AwardedCount,
			summary.IneligibleCount,
			summary.EligibleUnfundedCount,
			summary.EligibleUnfundedAmount,
			summary.EligibleRequestedTotal,
			summary.FullyFundedCount,
			summary.PartiallyFundedCount,
			summary.FundingGapTotal,
			summary.CoverageRate,
			summary.FullFundingRate,
			summary.AverageAward,
			summary.AwardP25,
			summary.AwardP50,
			summary.AwardP75,
			summary.AwardToRequestAvg,
			summary.MinAwarded,
			summary.MaxAwarded,
			summary.LastFundedPriority,
			summary.LastFundedScore,
			summary.LastFundedNeed,
			summary.LastFundedRequested,
			opts.MinAward,
			opts.MaxAward,
			opts.ScoreWeight,
			opts.NeedWeight,
			opts.ReserveHigh,
			opts.ReserveMedium,
			opts.ReserveLow,
			opts.RoundTo,
			opts.MaxPercent,
			opts.MinScore,
		).
		PlaceholderFormat(sq.Dollar)

	query, args, err := builder.ToSql()
	if err != nil {
		return fmt.Errorf("build run insert: %w", err)
	}
	if _, err := pool.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("insert run: %w", err)
	}
	return nil
}

func insertApplicants(ctx context.Context, pool *pgxpool.Pool, schema string, runID uuid.UUID, applicants []*applicant) error {
	if len(applicants) == 0 {
		return nil
	}
	const batchSize = 200
	for start := 0; start < len(applicants); start += batchSize {
		end := start + batchSize
		if end > len(applicants) {
			end = len(applicants)
		}
		builder := sq.Insert(schema+".applicants").
			Columns(
				"run_id",
				"applicant_id",
				"name",
				"need_level",
				"score_raw",
				"score_norm",
				"priority",
				"requested",
				"awarded",
				"eligible",
				"eligibility_msg",
			).
			PlaceholderFormat(sq.Dollar)

		for _, item := range applicants[start:end] {
			builder = builder.Values(
				runID,
				item.ID,
				item.Name,
				item.NeedLevel,
				item.ScoreRaw,
				item.ScoreNorm,
				item.PriorityScore,
				item.Requested,
				item.Awarded,
				item.Eligible,
				item.EligibilityMsg,
			)
		}

		query, args, err := builder.ToSql()
		if err != nil {
			return fmt.Errorf("build applicant insert: %w", err)
		}
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			return fmt.Errorf("insert applicants: %w", err)
		}
	}
	return nil
}

func insertNeedCoverage(ctx context.Context, pool *pgxpool.Pool, schema string, runID uuid.UUID, coverage map[string]needCoverageAgg) error {
	if len(coverage) == 0 {
		return nil
	}
	builder := sq.Insert(schema+".need_coverage").
		Columns(
			"run_id",
			"need_level",
			"eligible_count",
			"awarded_count",
			"unfunded_count",
			"requested_total",
			"awarded_total",
			"coverage_rate",
			"requested_share",
			"awarded_share",
			"share_delta",
		).
		PlaceholderFormat(sq.Dollar)

	levels := []string{"high", "medium", "low"}
	for _, level := range levels {
		agg, ok := coverage[level]
		if !ok {
			continue
		}
		builder = builder.Values(
			runID,
			level,
			agg.EligibleCount,
			agg.AwardedCount,
			agg.UnfundedCount,
			agg.RequestedTotal,
			agg.AwardedTotal,
			agg.CoverageRate,
			agg.RequestedShare,
			agg.AwardedShare,
			agg.ShareDelta,
		)
	}

	query, args, err := builder.ToSql()
	if err != nil {
		return fmt.Errorf("build need coverage insert: %w", err)
	}
	if _, err := pool.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("insert need coverage: %w", err)
	}
	return nil
}
