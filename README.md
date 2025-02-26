# Group Scholar Award Allocator

A Go-based CLI that ranks applicants and allocates scholarship awards against a fixed budget. It blends score and need weights, applies min/max award caps, and produces a concise operational summary with optional JSON output for reporting.

## Features
- Weighted prioritization using applicant score and need level
- Budget-aware allocation with min/max award caps
- Optional minimum score eligibility threshold
- Summary metrics by need level plus a ranked award list
- Coverage and unfunded demand signals, including unfunded lists
- Optional JSON export for dashboards or downstream analysis

## Usage

```bash
/opt/homebrew/bin/go run . \
  -input sample-applicants.csv \
  -budget 20000 \
  -min 500 \
  -max 5000 \
  -score-weight 0.7 \
  -need-weight 0.3 \
  -min-score 70 \
  -top 5 \
  -unfunded 5
```

To export JSON:

```bash
/opt/homebrew/bin/go run . \
  -input sample-applicants.csv \
  -budget 20000 \
  -json allocation.json
```

## CSV Schema

Required headers:
- `applicant_id`
- `score` (numeric)
- `need_level` (`low`, `medium`, `high`)
- `requested_amount` (numeric)

Optional headers:
- `name`

## Notes
- If `requested_amount` is below `-min`, the requested amount is honored.
- Applicants with invalid `need_level` or non-positive `requested_amount` are skipped.
- Use `-min-score` to exclude applicants below a minimum score from eligibility.
