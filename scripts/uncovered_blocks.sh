#!/usr/bin/env bash

set -euo pipefail

COVER_FILE="${1:-/tmp/coverage.out}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

go run "$SCRIPT_DIR/lib/uncovered_blocks.go" "$COVER_FILE" "true" ${@:-20}
