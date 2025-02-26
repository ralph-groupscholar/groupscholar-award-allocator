package main

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
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
	Applicants              int                        `json:"applicants"`
	AwardedCount            int                        `json:"awarded_count"`
	IneligibleCount         int                        `json:"ineligible_count"`
	EligibleUnfundedCount   int                        `json:"eligible_unfunded_count"`
	EligibleUnfundedAmount  float64                    `json:"eligible_unfunded_amount"`
	EligibleRequestedTotal  float64                    `json:"eligible_requested_total"`
	CoverageRate            float64                    `json:"coverage_rate"`
	AverageAward            float64                    `json:"average_award"`
	MinAwarded              float64                    `json:"min_awarded"`
	MaxAwarded              float64                    `json:"max_awarded"`
	ByNeed                  map[string]needAgg         `json:"by_need"`
	UnfundedByNeed          map[string]needUnfundedAgg `json:"unfunded_by_need"`
	IneligibleReasonSummary map[string]int             `json:"ineligible_reasons"`
	Awards                  []awardRecord              `json:"awards"`
	Unfunded                []awardRecord              `json:"unfunded"`
}

type needAgg struct {
	AwardedCount int     `json:"awarded_count"`
	BudgetUsed   float64 `json:"budget_used"`
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

func main() {
	inputPath := flag.String("input", "", "Path to applicant CSV file")
	budget := flag.Float64("budget", 0, "Total award budget")
	minAward := flag.Float64("min", 500, "Minimum award amount")
	maxAward := flag.Float64("max", 5000, "Maximum award amount")
	scoreWeight := flag.Float64("score-weight", 0.7, "Weight for applicant score (0-1)")
	needWeight := flag.Float64("need-weight", 0.3, "Weight for need level (0-1)")
	reserveHigh := flag.Float64("reserve-high", 0, "Share of budget reserved for high-need applicants (0-1)")
	roundTo := flag.Float64("round", 0, "Round awards to nearest increment (0 disables)")
	maxPercent := flag.Float64("max-percent", 1, "Max percent of requested amount to award (0-1]")
	minScore := flag.Float64("min-score", 0, "Minimum applicant score to be eligible")
	jsonPath := flag.String("json", "", "Optional path to write JSON output")
	topN := flag.Int("top", 10, "Number of awarded applicants to display")
	showAll := flag.Bool("all", false, "Show all awarded applicants")
	unfundedTop := flag.Int("unfunded", 10, "Number of unfunded eligible applicants to display")
	showAllUnfunded := flag.Bool("unfunded-all", false, "Show all unfunded eligible applicants")
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

	applicants, warnings, err := loadApplicants(*inputPath)
	if err != nil {
		exitWith(err.Error())
	}

	applyMinScore(applicants, *minScore)
	normalizeScores(applicants)
	assignPriority(applicants, *scoreWeight, *needWeight)
	sortApplicants(applicants)

	awarded := allocateBudget(applicants, *budget, *minAward, *maxAward, *reserveHigh, *roundTo, *maxPercent)
	if len(warnings) > 0 {
		fmt.Println("Warnings:")
		for _, warning := range warnings {
			fmt.Printf("- %s\n", warning)
		}
		fmt.Println()
	}

	summary := summarize(applicants, *budget, awarded)
	printSummary(summary)
	printAwards(awarded, *topN, *showAll)
	printUnfunded(summary.Unfunded, *unfundedTop, *showAllUnfunded)

	if *jsonPath != "" {
		if err := writeJSON(*jsonPath, summary, awarded); err != nil {
			exitWith(err.Error())
		}
		fmt.Printf("\nJSON written to %s\n", *jsonPath)
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

func allocateBudget(applicants []*applicant, budget, minAward, maxAward, reserveHigh, roundTo, maxPercent float64) []*applicant {
	remaining := budget
	var awarded []*applicant

	if reserveHigh > 0 {
		reserved := budget * reserveHigh
		reservedAwards := allocatePass(applicants, reserved, minAward, maxAward, roundTo, maxPercent, func(item *applicant) bool {
			return item.NeedLevel == "high"
		})
		awarded = append(awarded, reservedAwards...)
		usedReserved := totalAwarded(reservedAwards)
		remaining = budget - usedReserved
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
	var unfundedCount int
	var unfundedAmount float64
	var eligibleRequestedTotal float64
	if len(awarded) > 0 {
		minAward = awarded[0].Awarded
		maxAward = awarded[0].Awarded
	}

	for _, item := range applicants {
		if !item.Eligible {
			ineligibleCount++
			if item.EligibilityMsg != "" {
				ineligibleReasons[item.EligibilityMsg]++
			}
			continue
		}
		eligibleRequestedTotal += item.Requested
		if item.Awarded == 0 {
			unfundedCount++
			unfundedAmount += item.Requested
			agg := unfundedByNeed[item.NeedLevel]
			agg.Count++
			agg.Requested += item.Requested
			unfundedByNeed[item.NeedLevel] = agg
		}
	}

	for _, item := range awarded {
		budgetUsed += item.Awarded
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

	averageAward := 0.0
	if len(awarded) > 0 {
		averageAward = budgetUsed / float64(len(awarded))
	}
	coverageRate := 0.0
	if eligibleRequestedTotal > 0 {
		coverageRate = budgetUsed / eligibleRequestedTotal
	}

	return allocationSummary{
		GeneratedAt:             time.Now().Format(time.RFC3339),
		Budget:                  budget,
		BudgetUsed:              budgetUsed,
		BudgetLeft:              budget - budgetUsed,
		Applicants:              len(applicants),
		AwardedCount:            len(awarded),
		IneligibleCount:         ineligibleCount,
		EligibleUnfundedCount:   unfundedCount,
		EligibleUnfundedAmount:  unfundedAmount,
		EligibleRequestedTotal:  eligibleRequestedTotal,
		CoverageRate:            coverageRate,
		AverageAward:            averageAward,
		MinAwarded:              minAward,
		MaxAwarded:              maxAward,
		ByNeed:                  byNeed,
		UnfundedByNeed:          unfundedByNeed,
		IneligibleReasonSummary: ineligibleReasons,
		Awards:                  buildAwardRecords(awarded),
		Unfunded:                buildUnfundedRecords(applicants),
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

func printSummary(summary allocationSummary) {
	fmt.Println("Award Allocation Summary")
	fmt.Println(strings.Repeat("-", 26))
	fmt.Printf("Applicants:   %d\n", summary.Applicants)
	fmt.Printf("Awarded:      %d\n", summary.AwardedCount)
	fmt.Printf("Ineligible:   %d\n", summary.IneligibleCount)
	fmt.Printf("Eligible Unfunded: %d ($%.2f requested)\n", summary.EligibleUnfundedCount, summary.EligibleUnfundedAmount)
	fmt.Printf("Eligible Requested: $%.2f\n", summary.EligibleRequestedTotal)
	fmt.Printf("Coverage Rate: %.1f%%\n", summary.CoverageRate*100)
	fmt.Printf("Budget Used:  $%.2f\n", summary.BudgetUsed)
	fmt.Printf("Budget Left:  $%.2f\n", summary.BudgetLeft)
	fmt.Printf("Average Award $%.2f\n", summary.AverageAward)
	fmt.Printf("Award Range:  $%.2f - $%.2f\n", summary.MinAwarded, summary.MaxAwarded)
	printIneligibleReasons(summary.IneligibleReasonSummary)
	fmt.Println("\nBy Need Level")
	fmt.Println(strings.Repeat("-", 13))
	needKeys := []string{"high", "medium", "low"}
	for _, level := range needKeys {
		agg := summary.ByNeed[level]
		fmt.Printf("%s: %d awarded ($%.2f)\n", strings.Title(level), agg.AwardedCount, agg.BudgetUsed)
	}
	printUnfundedByNeed(summary.UnfundedByNeed)
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
