#!/usr/bin/env bash

set -euo pipefail

COVER_FILE="${1:-/tmp/coverage.out}"

cat <<'EOF'
# Uncovered Code Ranges (from Go coverage)
#
# Format:
#   <file>:(startLine,startCol)-(endLine,endCol)
#
# Field meanings:
#   <file>              Go source file path
#   startLine,startCol  Start position of an uncovered block (inclusive)
#   endLine,endCol      End position of an uncovered block (exclusive)
#
# Range semantics:
#   [startLine.startCol , endLine.endCol)
#
# Example:
#   foo/bar.go:(10,5)-(13,1)
#   Means:
#     - From line 10, column 5
#     - Through lines 11 and 12
#     - Stops before line 13, column 1
#

Details:
-----------------------------------------
EOF

awk '
NR == 1 { next }     # skip "mode: xxx"
$3 == 0 {
    split($1, a, ":")
    file = a[1]

    split(a[2], r, ",")
    split(r[1], s, ".")
    split(r[2], e, ".")

    printf "%s:(%d,%d)-(%d,%d)\n",
           file, s[1], s[2], e[1], e[2]
}
' "$COVER_FILE" | sed 's/github.com\/codetrek\/syntrix\///' | grep -vE '^cmd/|^api/' | sort | uniq
