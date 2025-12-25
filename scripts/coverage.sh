#!/usr/bin/env bash
# Generate Go test coverage report with summary and file output.
set -euo pipefail

# Optional output path override: COVERPROFILE=/path/to/coverage.out ./scripts/coverage.sh
COVERPROFILE=${COVERPROFILE:-"/tmp/coverage.out"}

# Default exclusion: skip command entrypoints under cmd/
DEFAULT_EXCLUDE='/cmd/'
EXTRA_EXCLUDE=${EXCLUDE_PATTERN:-}
EXCLUDE_REGEX="${DEFAULT_EXCLUDE}${EXTRA_EXCLUDE:+|${EXTRA_EXCLUDE}}"

PKGS=$(go list ./... | grep -Ev "${EXCLUDE_REGEX}")

echo "Running go test with coverage..."
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

echo -e "\nCoverage summary:"
go tool cover -func="$COVERPROFILE" | tail -n 1 | awk '{printf "%-10s %-15s %s\n", $1, $2, $3}'

go tool cover -html=$COVERPROFILE -o test_coverage.html
echo -e "\nTo view HTML report: go tool cover -html=$COVERPROFILE"
