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

## Iteration 6
- Added CSV export options for awarded and unfunded eligible applicants.
- Documented CSV export usage in the README.

## Iteration 7
- Added ineligible applicant exports in JSON and CSV, including eligibility reason details.
- Documented the new ineligible CSV output flag in the README.

## Iteration 8
- Added award distribution percentiles, average award-to-request rate, and last-funded cutoff metrics to the summary and JSON output.
- Extended database logging to persist the new allocation distribution and cutoff fields.

## Iteration 8
- Added need-level coverage metrics (eligible, awarded, requested, coverage rate) to the summary output and JSON.
- Logged need-level coverage snapshots to Postgres alongside allocation runs.

## Iteration 9
- Added budget shortfall/full-funding requirement metrics and need equity share deltas to the allocation summary and JSON output.
- Extended Postgres logging to persist budget requirement fields and need-level share deltas.

## Iteration 9
- Added Markdown report export for allocation summaries with awards, unfunded, and need coverage tables.
- Added Go test coverage for award calculation, reserve allocation, and summary metrics.

## Iteration 9
- Added per-need budget reserve shares for high/medium/low applicants with validation and database logging.
- Added allocation tests covering reserve share behavior.
- Updated README usage notes to document the new reserve options.

## Iteration 10
- Added scenario budget analysis with tabular console output, JSON inclusion, and Markdown report tables.
- Introduced scenario parsing and cloning helpers plus tests covering scenario metrics and parsing.
- Updated README to document scenario budget comparisons.
