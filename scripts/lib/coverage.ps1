param(
    [string]$Mode = "html",
    [string]$CoverProfile = "coverage.out",
    [int]$ExitCode = 0
)

$content = Get-Content $env:TMPFILE

$ok = $content |
    Where-Object { $_ -match '^ok' } |
    Where-Object { $_ -notmatch '^ok\s+tests/' } |
    ForEach-Object { $_ -replace 'of statements', '' -replace 'github.com/codetrek/syntrix/', '' }

$ok |
    Where-Object { $_ -match 'coverage:\s+\d+(\.\d+)?%' } |
    Sort-Object { [double](($_ -split '\s+')[4].TrimEnd('%')) } -Descending |
    ForEach-Object {
        $p = $_ -split '\s+'
        '{0,-3} {1,-40} {2,-10} {3,-10} {4}' -f $p[0], $p[1], $p[2], $p[3], $p[4]
    }

$other = $content |
    Where-Object { $_ -notmatch '^ok' } |
    ForEach-Object { $_ -replace 'github.com/codetrek/syntrix/', '' }

$other | ForEach-Object { $_ }

if ($ExitCode -ne 0) {
    exit $ExitCode
}

Write-Output ''
Write-Output 'Coverage summary:'
go tool cover -func="$CoverProfile" |
    Select-Object -Last 1 |
    ForEach-Object {
        $parts = $_ -split '\s+'
        ('{0,-10} {1,-15} {2}' -f $parts[0], $parts[1], $parts[2])
    }

go tool cover -html="$CoverProfile" -o test_coverage.html
Write-Output ''
Write-Output ('To view HTML report: go tool cover -html=' + $CoverProfile)

if ($Mode -ieq 'detail') {
    Write-Output ''
    Write-Output 'Function coverage details (excluding >= 85%):'
    Write-Output ('{0,-60} {1,-35} {2}' -f 'LOCATION', 'FUNCTION', 'COVERAGE')
    Write-Output ('-'*97)

    $funcData = go tool cover -func="$CoverProfile" |
        ForEach-Object { $_ -replace 'github.com/codetrek/syntrix/', '' }

    $count100 = 0
    $count95to100 = 0
    $count85to95 = 0
    $lowCoverage = @()
    $linesToPrint = @()

    $funcData |
        Where-Object { $_ -notmatch '^total:' } |
        ForEach-Object {
            $parts = $_ -split '\s+'
            if ($parts.Count -ge 3) {
                $covStr = $parts[-1]
                $covVal = [double]$covStr.TrimEnd('%')
                $loc = $parts[0]
                $fn = $parts[1]
                if ($covVal -eq 100.0) {
                    $count100++
                } elseif ($covVal -ge 95.0) {
                    $count95to100++
                } elseif ($covVal -ge 85.0) {
                    $count85to95++
                } else {
                    $line = ('{0,-60} {1,-35} {2}' -f $loc, $fn, $covStr)
                    $obj = New-Object PSObject -Property @{ Line = $line; Cov = $covVal }
                    $linesToPrint += $obj
                    if ($covVal -lt 70.0) {
                        $lowCoverage += $line
                    }
                }
            }
        }

    $linesToPrint | Sort-Object Cov -Descending | ForEach-Object { $_.Line }
    Write-Output ('-'*97)
    Write-Output ('Functions with 100% coverage: ' + $count100)
    Write-Output ('Functions with 95%-100% coverage: ' + $count95to100)
    Write-Output ('Functions with 85%-95% coverage: ' + $count85to95)

    if ($lowCoverage.Count -gt 0) {
        Write-Output ''
        Write-Output 'CRITICAL: Functions with < 70% coverage:'
        Write-Output ('-'*97)
        $lowCoverage | ForEach-Object { $_ }
        Write-Output ('-'*97)
    }
}
