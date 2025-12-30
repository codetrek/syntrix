#!/bin/bash

threshold_func=80.0
threshold_package=85.0
threshold_print=90.0
threshold_total=90.0

set -o pipefail
failed=0
# Run tests with coverage, excluding cmd/ directory
go test -covermode=atomic -coverprofile=coverage.out $(go list ./... | \
  grep -v "syntrix/cmd/|syntrix/api/") | \
  tee test_output.txt

sed -i 's/of statements//g; s/github.com\/codetrek\/syntrix\///g' test_output.txt

echo "---------------------------------------------------------------------------------------------------------"
echo -e "\nPackage coverage details:"
echo "-------------------------------"
cat test_output.txt | \
  grep "^ok" | grep -vE "^ok\s+tests/" | \
  awk -v threshold="$threshold_package" '
    $1 != "?" {
      cov = $5;
      sub("%", "", cov);
      if (cov + 0 < threshold + 0) {
        # Mark packages below package threshold with *
        printf "::error::%-3s %-40s %-10s %-10s %s (CRITICAL: < %s%%)\n", $1, $2, $3, $4, $5, threshold
        failed=1
      } else {
        printf "%-3s %-40s %-10s %-10s %s\n", $1, $2, $3, $4, $5
      }
    }
    END { if (failed) exit 1 }
  ' || failed=1

# Generate function report
go tool cover -func=coverage.out > coverage.txt

# Clean up package names for processing
sed 's/github.com\/codetrek\/syntrix\///g' coverage.txt > coverage.clean.txt

echo "---------------------------------------------------------------------------------------------------------"
echo -e "\nFunction coverage details (excluding >= ${threshold_print}%):"
printf "%-70s %-35s %s\n" "LOCATION" "FUNCTION" "COVERAGE"
echo "---------------------------------------------------------------------------------------------------------"

# Print functions with < 90% coverage, sorted
# Mark functions < 80% as error directly in the list
grep -v "^total:" coverage.clean.txt | \
awk -v threshold="$threshold_func" -v threshold_print="$threshold_print" '{
  cov = $3
  sub("%", "", cov)
  if (cov + 0 < threshold_print + 0) {
    if (cov + 0 < threshold + 0) {
      split($1, parts, ":")
      file = parts[1]
      line = parts[2]
      printf "::error file=%s,line=%s::%-70s %-35s %s (CRITICAL < %s%%)\n", file, line, $1, $2, $3, threshold
    } else {
      printf "%-70s %-35s %s\n", $1, $2, $3
    }
  }
}' | sort -k3 -nr

echo "---------------------------------------------------------------------------------------------------------"

# Print Total
grep "^total:" coverage.clean.txt | awk -v threshold_total="$threshold_total" '{
  if ($3 + 0 < threshold_total + 0) {
    # Mark total below threshold with *
    printf "::error::%-96s %s (CRITICAL: < %s%%)\n", "TOTAL", $3, threshold_total
    failed = 1
  } else {
    printf "%-96s %s\n", "TOTAL", $3
  }
}
END { if (failed) exit 1 }' || failed=1
echo "---------------------------------------------------------------------------------------------------------"

# Calculate and print groups
COUNT_100=$(grep -v "^total:" coverage.clean.txt | awk '$3 == "100.0%"' | wc -l)
COUNT_95=$(grep -v "^total:" coverage.clean.txt | awk '{cov=$3; sub("%", "", cov); if (cov + 0 >= 95.0 && cov + 0 < 100.0) print $0}' | wc -l)
COUNT_85=$(grep -v "^total:" coverage.clean.txt | awk '{cov=$3; sub("%", "", cov); if (cov + 0 >= 85.0 && cov + 0 < 95.0) print $0}' | wc -l)

echo "Statistics:"
echo "Functions with 100% coverage: $COUNT_100"
echo "Functions with 95%-100% coverage: $COUNT_95"
echo "Functions with 85%-95% coverage: $COUNT_85"

# Check should fail the script if any function is below function threshold
awk -v threshold_func="$threshold_func" -v failed="$failed" '
  BEGIN {
    failed = failed  + 0
  }
  $1 != "total:" {
    cov = $3
    sub("%", "", cov)
    if (cov + 0 < threshold_func + 0) {
      failed = 1
    }
  }
  END {
    if (failed) exit 1
  }
' coverage.clean.txt
