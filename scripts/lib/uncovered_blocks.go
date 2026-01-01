package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

const MAX_GAP_LINES = 20
const CRITICAL_EFFECTIVE_LINES = 4

var maxOutputBlocks = 40

type FileCache struct {
	files map[string][]string
}

func NewFileCache() *FileCache {
	return &FileCache{
		files: make(map[string][]string),
	}
}

func (fc *FileCache) GetLines(path string) ([]string, error) {
	if lines, ok := fc.files[path]; ok {
		return lines, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	fc.files[path] = lines
	return lines, nil
}

func (fc *FileCache) AreLinesIgnorable(path string, startLine, endLine int) bool {
	// startLine and endLine are 1-based
	lines, err := fc.GetLines(path)
	if err != nil {
		// If we can't read the file, assume not ignorable to be safe
		return false
	}

	if startLine > endLine {
		return true
	}

	for i := startLine; i <= endLine; i++ {
		if i < 1 || i > len(lines) {
			// Line out of bounds, treat as not ignorable or ignore?
			// If out of bounds, it shouldn't happen if coverage is correct.
			return false
		}
		lineContent := strings.TrimSpace(lines[i-1])
		if lineContent == "" {
			continue
		}
		if strings.HasPrefix(lineContent, "//") {
			continue
		}
		// Found a non-empty, non-comment line
		return false
	}
	return true
}

func (fc *FileCache) AnalyzeBlock(b *MergedBlock) {
	lines, err := fc.GetLines(b.File)
	if err != nil {
		b.Level = "UNKNOWN"
		return
	}

	var effectiveLines []string
	for i := b.StartLine; i <= b.EndLine; i++ {
		if i < 1 || i > len(lines) {
			continue
		}
		line := strings.TrimSpace(lines[i-1])
		if isLineIgnorable(line) {
			continue
		}
		effectiveLines = append(effectiveLines, line)
	}

	b.EffectiveLines = len(effectiveLines)

	hasLog := false
	hasError := false
	hasLogic := false

	for _, line := range effectiveLines {
		if isLogic(line) {
			hasLogic = true
			continue
		}
		if isLog(line) {
			hasLog = true
			continue
		}
		if isError(line) {
			hasError = true
			continue
		}
		// Ignore closing braces/parens for logic detection
		if isLineIgnorable(line) {
			continue
		}
		hasLogic = true
	}

	if hasLogic {
		if len(effectiveLines) >= CRITICAL_EFFECTIVE_LINES {
			b.Level = "CRITICAL"
			b.FixAction = "\033[31mRequired\033[0m"
		} else {
			b.Level = "HIGH"
			b.FixAction = "Suggested"
		}
	} else if hasError {
		b.Level = "MEDIUM"
		b.FixAction = "Consider"
	} else if hasLog {
		b.Level = "LOW"
	} else {
		b.Level = "LOW"
	}
}

func isLineIgnorable(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "//") || line == "}" || line == "){" || line == ")" || line == "]" || line == "}," {
		return true
	}
	return false
}

func isLog(line string) bool {
	// Check for common logging patterns
	if strings.HasPrefix(line, "log.") ||
		strings.HasPrefix(line, "fmt.Print") ||
		strings.Contains(line, ".Log") ||
		strings.Contains(line, ".Info") ||
		strings.Contains(line, ".Debug") ||
		strings.Contains(line, ".Warn") ||
		strings.Contains(line, ".Error") ||
		strings.Contains(line, "t.Log") ||
		strings.Contains(line, "t.Skip") {
		return true
	}
	return false
}

func isLogic(line string) bool {
	logicKeywords := []string{
		"if", "for", "switch", "case", "return", "break", "continue", "goto",
		"func", "defer", "select", "chan", "map", "struct", "interface",
	}
	for _, kw := range logicKeywords {
		if strings.Contains(line, kw) {
			return true
		}
	}
	return false
}

func isError(line string) bool {
	patterns := [][]string{
		{"if", "err", "!="},
		{"return", "err"},
		{"http.Error"},
		{"fmt.Errorf"},
		{"errors.New"},
		{"writeError"},
	}
	for _, pattern := range patterns {
		if matchesPattern(line, pattern) {
			return true
		}
	}
	return false
}

func matchesPattern(line string, pattern []string) bool {
	for _, p := range pattern {
		if strings.Contains(line, p) {
			return true
		}
	}
	return false
}

type Block struct {
	File      string
	StartLine int
	StartCol  int
	EndLine   int
	EndCol    int
	Count     int
}

type MergedBlock struct {
	File           string
	StartLine      int
	StartCol       int
	EndLine        int
	EndCol         int
	NumLines       int
	EffectiveLines int
	Level          string
	FixAction      string
}

func (b *MergedBlock) ShouldPrint() bool {
	return b.Level != "LOW"
}

func (b *MergedBlock) Print(locWidth int) {
	if b.ShouldPrint() {
		rangeStr := fmt.Sprintf("%s:%d:%d-%d:%d", b.File, b.StartLine, b.StartCol, b.EndLine, b.EndCol)
		linesStr := fmt.Sprintf("%d", b.NumLines)

		if os.Getenv("CI") == "true" && b.Level == "CRITICAL" {
			fmt.Printf("::error file=%s,line=%d::", b.File, b.StartLine)
		}
		fmt.Printf("%-*s %-6s %-6d %-10s %s\n", locWidth, rangeStr, linesStr, b.EffectiveLines, b.Level, b.FixAction)
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run uncovered_blocks.go <coverage_file> [print_description or not] [print_line_count]")
		os.Exit(1)
	}

	coverFile := os.Args[1]
	printDescription := os.Args[2] == "true" // Any extra args
	printLineCount := os.Args[3]
	file, err := os.Open(coverFile)
	if err != nil {
		fmt.Printf("Error opening file: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	if n, err := strconv.Atoi(printLineCount); err == nil {
		if n > 0 && n < 500 {
			maxOutputBlocks = n
		}
	}

	var blocks []Block
	scanner := bufio.NewScanner(file)

	// Skip mode line
	if scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "mode:") {
			// If not mode line, process it (though usually first line is mode)
			if b, ok := parseLine(line); ok {
				blocks = append(blocks, b)
			}
		}
	}

	for scanner.Scan() {
		if b, ok := parseLine(scanner.Text()); ok {
			blocks = append(blocks, b)
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("Error reading file: %v\n", err)
		os.Exit(1)
	}

	merged := mergeBlocks(blocks)

	// Analyze blocks
	fileCache := NewFileCache()
	for i := range merged {
		fileCache.AnalyzeBlock(&merged[i])
	}

	// Sort by Level (CRITICAL > HIGH > MEDIUM > LOW) then by NumLines descending
	levelWeight := map[string]int{
		"CRITICAL": 4,
		"HIGH":     3,
		"MEDIUM":   2,
		"LOW":      1,
	}

	sort.Slice(merged, func(i, j int) bool {
		w1 := levelWeight[merged[i].Level]
		w2 := levelWeight[merged[j].Level]
		if w1 != w2 {
			return w1 > w2
		}
		if merged[i].EffectiveLines != merged[j].EffectiveLines {
			return merged[i].EffectiveLines > merged[j].EffectiveLines
		}
		return merged[i].NumLines > merged[j].NumLines
	})

	// Calculate max location width
	maxLocWidth := 0
	for _, b := range merged {
		if !b.ShouldPrint() {
			continue
		}
		loc := fmt.Sprintf("%s:%d:%d-%d:%d", b.File, b.StartLine, b.StartCol, b.EndLine, b.EndCol)
		if len(loc) > maxLocWidth {
			maxLocWidth = len(loc)
		}
	}
	if maxLocWidth < 20 {
		maxLocWidth = 20
	}

	printHeader(maxLocWidth, printDescription)
	limit := maxOutputBlocks
	count := 0
	printedNonCritical := false

	for _, b := range merged {
		if !b.ShouldPrint() {
			continue
		}

		isCritical := b.Level == "CRITICAL"

		// Condition to print:
		// 1. It is CRITICAL (always print)
		// 2. We haven't reached the limit yet
		// 3. We haven't printed any non-critical yet (ensure at least one)
		shouldPrint := isCritical || count < limit || !printedNonCritical

		if shouldPrint {
			b.Print(maxLocWidth)
			count++
			if !isCritical {
				printedNonCritical = true
			}
		} else {
			// If we are here, it means:
			// - It's not critical
			// - We reached the limit
			// - We already printed at least one non-critical
			// So we can stop, because the list is sorted by Level then Lines.
			// Remaining items will also be non-critical (or lower priority).
			break
		}
	}

	remaining := 0
	for i := 0; i < len(merged); i++ {
		if merged[i].ShouldPrint() {
			remaining++
		}
	}
	remaining -= count

	if remaining > 0 {
		fmt.Printf("... %d more ...\n", remaining)
	}
}

func parseLine(line string) (Block, bool) {
	// Format: file:startLine.startCol,endLine.endCol numStmt count
	parts := strings.Fields(line)
	if len(parts) != 3 {
		return Block{}, false
	}

	locParts := strings.Split(parts[0], ":")
	if len(locParts) != 2 {
		return Block{}, false
	}
	filePath := locParts[0]

	// Filter out prefix
	filePath = strings.TrimPrefix(filePath, "github.com/codetrek/syntrix/")

	rangeParts := strings.Split(locParts[1], ",")
	if len(rangeParts) != 2 {
		return Block{}, false
	}

	startParts := strings.Split(rangeParts[0], ".")
	endParts := strings.Split(rangeParts[1], ".")

	startLine, _ := strconv.Atoi(startParts[0])
	startCol, _ := strconv.Atoi(startParts[1])
	endLine, _ := strconv.Atoi(endParts[0])
	endCol, _ := strconv.Atoi(endParts[1])

	count, _ := strconv.Atoi(parts[2])

	return Block{
		File:      filePath,
		StartLine: startLine,
		StartCol:  startCol,
		EndLine:   endLine,
		EndCol:    endCol,
		Count:     count,
	}, true
}

func mergeBlocks(blocks []Block) []MergedBlock {
	var result []MergedBlock
	var current *MergedBlock
	fileCache := NewFileCache()

	for _, b := range blocks {
		if b.Count > 0 {
			if current != nil {
				// Finish current block
				current.NumLines = current.EndLine - current.StartLine + 1
				result = append(result, *current)
				current = nil
			}
			continue
		}

		// It is an uncovered block (Count == 0)
		if current == nil {
			current = &MergedBlock{
				File:      b.File,
				StartLine: b.StartLine,
				StartCol:  b.StartCol,
				EndLine:   b.EndLine,
				EndCol:    b.EndCol,
			}
		} else {
			// Try to merge
			shouldMerge := false
			if b.File == current.File {
				diff := b.StartLine - current.EndLine
				if diff <= 1 {
					// Adjacent or overlapping lines (diff <= 1 means gap is 0 lines)
					shouldMerge = true
				} else {
					// Check gap lines
					gapLinesCount := diff - 1
					if gapLinesCount <= MAX_GAP_LINES {
						// Check if gap lines are ignorable (empty or comments)
						// Gap lines are from (current.EndLine + 1) to (b.StartLine - 1)
						if fileCache.AreLinesIgnorable(b.File, current.EndLine+1, b.StartLine-1) {
							shouldMerge = true
						}
					}
				}
			}

			if shouldMerge {
				current.EndLine = b.EndLine
				current.EndCol = b.EndCol
			} else {
				// Cannot merge, save current and start new
				current.NumLines = current.EndLine - current.StartLine + 1
				result = append(result, *current)

				current = &MergedBlock{
					File:      b.File,
					StartLine: b.StartLine,
					StartCol:  b.StartCol,
					EndLine:   b.EndLine,
					EndCol:    b.EndCol,
				}
			}
		}
	}

	if current != nil {
		current.NumLines = current.EndLine - current.StartLine + 1
		result = append(result, *current)
	}

	return result
}

func printHeader(locWidth int, printDescription bool) {
	if printDescription {
		fmt.Println(`# Uncovered Code Ranges (from Go coverage)
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
#   foo/bar.go:(10,5)-(13,1) 3
#   Means:
#     - From line 10, column 5
#     - Through lines 11 and 12
#     - Stops before line 13, column 1
#     - 3 lines uncovered
#

Details:`)
	} else {
		fmt.Println("# Uncovered Code Ranges (from Go coverage)")
	}
	separator := strings.Repeat("-", locWidth+30)
	fmt.Println(separator)
	fmt.Printf("%-*s %-6s %-6s %-10s %s\n", locWidth, "LOCATION", "LINES", "EFFECT", "STATUS", "FIX ACTION")
	fmt.Println(separator)
}
