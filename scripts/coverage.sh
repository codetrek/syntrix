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

threshold_func=80.0
threshold_package=85.0
threshold_print=90.0
threshold_total=90.0

echo "Excluding packages matching: ${EXCLUDE_REGEX}"
# Use a temp file to capture output for sorting
TMP_OUTPUT=$(mktemp)
trap 'rm -f "$TMP_OUTPUT"' EXIT

# Run tests, allow failure to capture output, but record exit code
set +e
go test ${PKGS} -covermode=atomic -coverprofile="$COVERPROFILE" > "$TMP_OUTPUT" 2>&1
EXIT_CODE=$?
set -e

sed -i 's/of statements//g; s/github.com\/codetrek\/syntrix\///g' "$TMP_OUTPUT"

# Process and sort 'ok' lines (coverage data)
grep "^ok" "$TMP_OUTPUT" | \
    grep -vE "^ok\s+tests/" | \
    awk -v threshold_package="$threshold_package" '{
        cov = $5;
        sub("%", "", cov);
        if (cov + 0 < threshold_package + 0) {
            # Mark packages below package threshold with *
            printf "%-3s %-40s %-10s %-10s \033[31m%s\033[0m (CRITICAL: < %s%%)\n", $1, $2, $3, $4, $5, threshold_package
        } else {
            printf "%-3s %-40s %-10s %-10s %s\n", $1, $2, $3, $4, $5
        }
    }' | \
    sort -k5 -nr || true

# Process and print other lines (skipped, failures, etc.)
grep -v "^ok" "$TMP_OUTPUT" | \
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

echo -e "\nFunction coverage details (excluding >= ${threshold_print}%):"
printf "%-60s %-35s %s\n" "LOCATION" "FUNCTION" "COVERAGE"
echo "---------------------------------------------------------------------------------------------------------"

FUNC_DATA=$(go tool cover -func="$COVERPROFILE" | sed 's/github.com\/codetrek\/syntrix\///g')

go tool cover -html=$COVERPROFILE -o test_coverage.html

# Print functions with < 85% coverage
echo "$FUNC_DATA" | \
    grep -v "^total:" | \
    awk -v threshold_print="$threshold_print" -v threshold_func="$threshold_func" '{
        cov = $3;
        sub("%", "", cov);
        if (cov + 0 < threshold_func + 0) {
            # Mark functions below function threshold with *
            printf "%-60s %-35s \033[31m%s\033[0m (CRITICAL: < %s%%)\n", $1, $2, $3, threshold_func
        } else if (cov + 0 < threshold_print + 0) {
            printf "%-60s %-35s %s\n", $1, $2, $3
        }
    }' | \
    sort -k3 -nr

echo "---------------------------------------------------------------------------------------------------------"

echo "$FUNC_DATA" | \
    grep "^total:" | \
    awk -v threshold_total="$threshold_total" '{
        if ($3 + 0 < threshold_total + 0) {
            # Mark total below threshold with *
            printf "%-96s \033[31m%s\033[0m (CRITICAL: < %s%%)\n", "TOTAL", $3, threshold_total
        } else {
            printf "%-96s %s\n", "TOTAL", $3
        }
    }'

echo "---------------------------------------------------------------------------------------------------------"

COUNT_100=$(echo "$FUNC_DATA" | awk '$3 == "100.0%"' | wc -l)
COUNT_95_100=$(echo "$FUNC_DATA" | awk '{cov=$3; sub("%", "", cov); if (cov + 0 >= 95.0 && cov + 0 < 100.0) print $0}' | wc -l)
COUNT_85_95=$(echo "$FUNC_DATA" | awk '{cov=$3; sub("%", "", cov); if (cov + 0 >= 85.0 && cov + 0 < 95.0) print $0}' | wc -l)
COUNT_LE_85=$(echo "$FUNC_DATA" | awk '{cov=$3; sub("%", "", cov); if (cov + 0 < 85.0) print $0}' | wc -l)

echo "Statistics:"
echo "Functions with 100% coverage: $COUNT_100"
echo "Functions with 95%-100% coverage: $COUNT_95_100"
echo "Functions with 85%-95% coverage: $COUNT_85_95"
echo "Functions with <85% coverage: $COUNT_LE_85"
