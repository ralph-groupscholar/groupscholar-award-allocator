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
	GeneratedAt  string             `json:"generated_at"`
	Budget       float64            `json:"budget"`
	BudgetUsed   float64            `json:"budget_used"`
	BudgetLeft   float64            `json:"budget_left"`
	Applicants   int                `json:"applicants"`
	AwardedCount int                `json:"awarded_count"`
	AverageAward float64            `json:"average_award"`
	MinAwarded   float64            `json:"min_awarded"`
	MaxAwarded   float64            `json:"max_awarded"`
	ByNeed       map[string]needAgg `json:"by_need"`
	Awards       []awardRecord      `json:"awards"`
}

type needAgg struct {
	AwardedCount int     `json:"awarded_count"`
	BudgetUsed   float64 `json:"budget_used"`
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
	jsonPath := flag.String("json", "", "Optional path to write JSON output")
	topN := flag.Int("top", 10, "Number of awarded applicants to display")
	showAll := flag.Bool("all", false, "Show all awarded applicants")
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
	weightTotal := *scoreWeight + *needWeight
	if weightTotal == 0 {
		exitWith("score-weight and need-weight cannot both be zero")
	}

	applicants, warnings, err := loadApplicants(*inputPath)
	if err != nil {
		exitWith(err.Error())
	}

	normalizeScores(applicants)
	assignPriority(applicants, *scoreWeight, *needWeight)
	sortApplicants(applicants)

	awarded := allocateBudget(applicants, *budget, *minAward, *maxAward)
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
		applicant.Eligible = false
		applicant.EligibilityMsg = "requested_amount must be > 0"
	}
	if need != "low" && need != "medium" && need != "high" {
		applicant.Eligible = false
		applicant.EligibilityMsg = "need_level must be low, medium, or high"
	}

	return applicant, ""
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

func allocateBudget(applicants []*applicant, budget, minAward, maxAward float64) []*applicant {
	remaining := budget
	var awarded []*applicant

	for _, item := range applicants {
		if !item.Eligible {
			continue
		}
		award := clamp(item.Requested, minAward, maxAward)
		if item.Requested < minAward {
			award = item.Requested
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

func clamp(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func summarize(applicants []*applicant, budget float64, awarded []*applicant) allocationSummary {
	byNeed := map[string]needAgg{
		"low":    {},
		"medium": {},
		"high":   {},
	}

	var budgetUsed float64
	var minAward float64
	var maxAward float64
	if len(awarded) > 0 {
		minAward = awarded[0].Awarded
		maxAward = awarded[0].Awarded
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

	return allocationSummary{
		GeneratedAt:  time.Now().Format(time.RFC3339),
		Budget:       budget,
		BudgetUsed:   budgetUsed,
		BudgetLeft:   budget - budgetUsed,
		Applicants:   len(applicants),
		AwardedCount: len(awarded),
		AverageAward: averageAward,
		MinAwarded:   minAward,
		MaxAwarded:   maxAward,
		ByNeed:       byNeed,
		Awards:       buildAwardRecords(awarded),
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

func printSummary(summary allocationSummary) {
	fmt.Println("Award Allocation Summary")
	fmt.Println(strings.Repeat("-", 26))
	fmt.Printf("Applicants:   %d\n", summary.Applicants)
	fmt.Printf("Awarded:      %d\n", summary.AwardedCount)
	fmt.Printf("Budget Used:  $%.2f\n", summary.BudgetUsed)
	fmt.Printf("Budget Left:  $%.2f\n", summary.BudgetLeft)
	fmt.Printf("Average Award $%.2f\n", summary.AverageAward)
	fmt.Printf("Award Range:  $%.2f - $%.2f\n", summary.MinAwarded, summary.MaxAwarded)
	fmt.Println("\nBy Need Level")
	fmt.Println(strings.Repeat("-", 13))
	needKeys := []string{"high", "medium", "low"}
	for _, level := range needKeys {
		agg := summary.ByNeed[level]
		fmt.Printf("%s: %d awarded ($%.2f)\n", strings.Title(level), agg.AwardedCount, agg.BudgetUsed)
	}
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
