#!/usr/bin/env bash
# Generate Go test coverage report with summary and file output.
set -euo pipefail

# Optional output path override: COVERPROFILE=/path/to/coverage.out ./scripts/coverage.sh
COVERPROFILE=${COVERPROFILE:-"/tmp/coverage.out"}

# Default exclusion: skip command entrypoints under cmd/
DEFAULT_EXCLUDE='^syntrix/cmd'
EXTRA_EXCLUDE=${EXCLUDE_PATTERN:-}
EXCLUDE_REGEX="${DEFAULT_EXCLUDE}${EXTRA_EXCLUDE:+|${EXTRA_EXCLUDE}}"

PKGS=$(go list ./... | grep -Ev "${EXCLUDE_REGEX}")

echo "Running go test with coverage..."
echo "Excluding packages matching: ${EXCLUDE_REGEX}"
go test ${PKGS} -covermode=atomic -coverprofile="$COVERPROFILE"

echo -e "\nCoverage summary:"
go tool cover -func="$COVERPROFILE" | tail -n 1

go tool cover -html=$COVERPROFILE -o test_coverage.html
echo -e "\nTo view HTML report: go tool cover -html=$COVERPROFILE"
