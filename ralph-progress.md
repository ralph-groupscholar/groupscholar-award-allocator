# Ralph Progress Log

## Iteration 1
- Created the Group Scholar Award Allocator Go CLI with weighted prioritization, budget-aware allocations, and JSON export support.
- Added a sample applicant CSV and documented usage plus schema notes in the README.

## Iteration 2
- Added coverage metrics, unfunded-by-need summaries, and unfunded applicant lists for stronger demand visibility.
- Expanded CLI options and README examples to surface unfunded rankings alongside awards.

## Iteration 3
- Added an optional minimum score eligibility threshold to filter applicants below a configurable score.
- Updated CLI usage documentation to cover the new eligibility guardrail.

## Iteration 4
- Added optional Postgres logging for allocation runs, with schema-safe setup and batch applicant inserts.
- Documented database logging env vars and CLI flag usage in the README.

## Iteration 5
- Added eligible counts plus full/partial funding metrics, funding gap totals, and full funding rate calculations.
- Extended database run logging to persist the new allocation health metrics.

## Iteration 5
- Added CSV export options for awarded and unfunded eligible applicants.
- Documented CSV export usage in the README.
