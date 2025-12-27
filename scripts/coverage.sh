#!/usr/bin/env bash
# Generate Go test coverage report.
# Usage: ./scripts/coverage.sh [html|func]
#   html: (default) Generate HTML report and print summary.
#   func: Print function-level coverage details.

set -euo pipefail

MODE=${1:-html}

# Optional output path override: COVERPROFILE=/path/to/coverage.out ./scripts/coverage.sh
COVERPROFILE=${COVERPROFILE:-"/tmp/coverage.out"}

# Default exclusion: skip command entrypoints under cmd/
DEFAULT_EXCLUDE='/cmd/'
EXTRA_EXCLUDE=${EXCLUDE_PATTERN:-}
EXCLUDE_REGEX="${DEFAULT_EXCLUDE}${EXTRA_EXCLUDE:+|${EXTRA_EXCLUDE}}"

PKGS=$(go list ./... | grep -Ev "${EXCLUDE_REGEX}")

echo "Excluding packages matching: ${EXCLUDE_REGEX}"
# Use a temp file to capture output for sorting
TMP_OUTPUT=$(mktemp)
trap 'rm -f "$TMP_OUTPUT"' EXIT

# Run tests, allow failure to capture output, but record exit code
set +e
go test ${PKGS} -covermode=atomic -coverprofile="$COVERPROFILE" > "$TMP_OUTPUT" 2>&1
EXIT_CODE=$?
set -e

# Process and sort 'ok' lines (coverage data)
grep "^ok" "$TMP_OUTPUT" | \
    sed 's/of statements//g; s/github.com\/codetrek\/syntrix\///g' | \
    awk '{ printf "%-3s %-40s %-10s %-10s %s\n", $1, $2, $3, $4, $5 }' | \
    sort -k5 -nr || true

# Process and print other lines (skipped, failures, etc.)
grep -v "^ok" "$TMP_OUTPUT" | \
    sed 's/github.com\/codetrek\/syntrix\///g' | \
    awk '{
        if ($1 == "?") {
             printf "%-3s %-40s %s %s %s\n", $1, $2, $3, $4, $5
        } else {
            print $0
        }
    }' || true

if [ $EXIT_CODE -ne 0 ]; then
    exit $EXIT_CODE
fi

if [ "$MODE" == "func" ]; then
    echo -e "\nFunction coverage details (excluding >= 85%):"
    printf "%-60s %-35s %s\n" "LOCATION" "FUNCTION" "COVERAGE"
    echo "---------------------------------------------------------------------------------------------------------"

    FUNC_DATA=$(go tool cover -func="$COVERPROFILE" | sed 's/github.com\/codetrek\/syntrix\///g')

    # Print functions with < 85% coverage
    echo "$FUNC_DATA" | \
        grep -v "^total:" | \
        awk '{
            cov = $3;
            sub("%", "", cov);
            if (cov < 85.0) {
                printf "%-60s %-35s %s\n", $1, $2, $3
            }
        }' | \
        sort -k3 -nr

    echo "---------------------------------------------------------------------------------------------------------"

    COUNT_100=$(echo "$FUNC_DATA" | awk '$3 == "100.0%"' | wc -l)
    COUNT_95_100=$(echo "$FUNC_DATA" | awk '{cov=$3; sub("%", "", cov); if (cov >= 95.0 && cov < 100.0) print $0}' | wc -l)
    COUNT_85_95=$(echo "$FUNC_DATA" | awk '{cov=$3; sub("%", "", cov); if (cov >= 85.0 && cov < 95.0) print $0}' | wc -l)

    echo "Functions with 100% coverage: $COUNT_100"
    echo "Functions with 95%-100% coverage: $COUNT_95_100"
    echo "Functions with 85%-95% coverage: $COUNT_85_95"

    # Check for < 60% coverage
    LOW_COVERAGE=$(echo "$FUNC_DATA" | grep -v "^total:" | awk '{cov=$3; sub("%", "", cov); if (cov < 60.0) printf "%-60s %-35s %s\n", $1, $2, $3}')

    if [ ! -z "$LOW_COVERAGE" ]; then
        echo -e "\nCRITICAL: Functions with < 60% coverage:"
        echo "---------------------------------------------------------------------------------------------------------"
        echo "$LOW_COVERAGE"
        echo "---------------------------------------------------------------------------------------------------------"
    fi

    echo "$FUNC_DATA" | \
        grep "^total:" | \
        awk '{printf "%-96s %s\n", "TOTAL", $3}'
else
    echo -e "\nCoverage summary:"
    go tool cover -func="$COVERPROFILE" | tail -n 1 | awk '{printf "%-10s %-15s %s\n", $1, $2, $3}'

    go tool cover -html=$COVERPROFILE -o test_coverage.html
    echo -e "\nTo view HTML report: go tool cover -html=$COVERPROFILE"
fi
