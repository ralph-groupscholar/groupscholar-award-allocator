# Group Scholar Award Allocator

A Go-based CLI that ranks applicants and allocates scholarship awards against a fixed budget. It blends score and need weights, applies min/max award caps, and produces a concise operational summary with optional JSON output for reporting.

## Features
- Weighted prioritization using applicant score and need level
- Budget-aware allocation with min/max award caps
- Optional minimum score eligibility threshold
- Summary metrics by need level plus a ranked award list
- Coverage and unfunded demand signals, including unfunded lists
- Optional JSON export for dashboards or downstream analysis
- Optional CSV exports for awarded and unfunded cohorts

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

To export CSVs:

```bash
/opt/homebrew/bin/go run . \
  -input sample-applicants.csv \
  -budget 20000 \
  -awards-csv awarded.csv \
  -unfunded-csv unfunded.csv
```

## Database Logging (Optional)

Enable run logging to Postgres for longitudinal analysis.

Set environment variables:

```bash
export GS_AWARD_ALLOCATOR_DB_URL="postgres://<user>:<password>@<host>:<port>/<db>?sslmode=require"
export GS_AWARD_ALLOCATOR_SCHEMA="gs_award_allocator"
```

Then run with `-db-log`:

```bash
/opt/homebrew/bin/go run . \
  -input sample-applicants.csv \
  -budget 20000 \
  -db-log
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
