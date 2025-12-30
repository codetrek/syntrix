@echo off
setlocal

:: Mode: summary (default) or detail
set MODE=%1
if "%MODE%"=="" set MODE=summary

:: COVERPROFILE can be overridden via env COVERPROFILE
if "%COVERPROFILE%"=="" (
    set COVERPROFILE=coverage.out
)

set EXCLUDE_REGEX=/cmd/

:: Build package list excluding cmd/
for /f "usebackq tokens=*" %%i in (`powershell -NoProfile -Command "(go list ./... | Where-Object { $_ -notmatch '%EXCLUDE_REGEX%' }) -join ' '"`) do set PKGS=%%i

if "%PKGS%"=="" (
    echo No packages found to test.
    exit /b 1
)

echo Excluding packages matching: %EXCLUDE_REGEX%

:: Temp file for capturing output
for /f "usebackq" %%i in (`powershell -NoProfile -Command "New-TemporaryFile | Select-Object -ExpandProperty FullName"`) do set TMPFILE=%%i

:: Run go test with coverage and capture output
go test %PKGS% -covermode=atomic -coverprofile="%COVERPROFILE%" >"%TMPFILE%" 2>&1
set EXITCODE=%ERRORLEVEL%

:: Process ok lines: strip module prefix and sort by coverage descending (column 5)
set SCRIPT_DIR=%~dp0
set SCRIPT_PATH=%SCRIPT_DIR%lib\coverage.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File "%SCRIPT_PATH%" -Mode "%MODE%" -CoverProfile "%COVERPROFILE%" -ExitCode %EXITCODE%

del "%TMPFILE%"

endlocal
