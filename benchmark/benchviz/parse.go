package main

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// BenchResult holds a single benchmark measurement.
type BenchResult struct {
	Name     string  // full benchmark name (e.g. "Benchmark_Marshal_Tiny_StdJSON-16")
	Group    string  // dataset group (e.g. "Marshal_Tiny")
	Library  string  // library name (e.g. "StdJSON", "Sonic", "Velox")
	NsOp     float64 // nanoseconds per operation
	BOp      float64 // bytes allocated per operation
	AllocsOp float64 // allocations per operation
}

// GroupResult holds the aggregated (median) result for a library within a group.
type GroupResult struct {
	Group    string
	Library  string
	NsOp     float64
	BOp      float64
	AllocsOp float64
}

// Section represents a top-level benchmark category (e.g. "Marshal", "Parallel Unmarshal").
type Section struct {
	Name   string   // display name (e.g. "Parallel Marshal")
	Groups []string // ordered group names belonging to this section
}

// BenchData holds all parsed and aggregated benchmark data.
type BenchData struct {
	Title    string                             // from -title flag or auto-detected
	Subtitle string                             // goos/goarch/cpu metadata
	Groups   []string                           // ordered list of unique groups
	Sections []Section                          // ordered sections, each containing its groups
	Libs     []string                           // ordered list of unique libraries
	Results  map[string]map[string]*GroupResult // group -> library -> result
	RunCount int                                // number of runs per benchmark (from -count)
}


// splitGroupToSection splits a group name following the pattern "{Action}_{Dataset}".
// Examples:
//   - "Marshal_Twitter" -> section="Marshal", dataset="Twitter"
//   - "ParallelMarshal_KubePods" -> section="Parallel Marshal", dataset="KubePods"
//   - "Decoder_Small" -> section="Decoder", dataset="Small"
//
// This matches the naming convention used in benchmark functions:
//
//	Benchmark_{Action}_{Dataset}_{Library}
func splitGroupToSection(group string) (section, dataset string) {
	// Find the last underscore to identify dataset
	idx := strings.LastIndex(group, "_")
	if idx <= 0 {
		// No underscore or underscore at start: treat whole string as section
		return group, group
	}

	action := group[:idx]
	dataset = group[idx+1:]

	// Format action name for display: insert space before capital letters in camelCase
	// e.g., "ParallelMarshal" -> "Parallel Marshal"
	section = formatActionName(action)

	return section, dataset
}

// formatActionName converts camelCase action names to spaced display names.
// Examples: "ParallelMarshal" -> "Parallel Marshal", "Marshal" -> "Marshal"
func formatActionName(action string) string {
	if action == "" {
		return action
	}

	var result strings.Builder
	for i, r := range action {
		// Insert space before uppercase letter (except at start)
		if i > 0 && r >= 'A' && r <= 'Z' {
			result.WriteRune(' ')
		}
		result.WriteRune(r)
	}

	return result.String()
}

// benchLineRe matches a standard Go benchmark output line.
// The optional MB/s field can appear between ns/op and B/op.
var benchLineRe = regexp.MustCompile(
	`^(Benchmark\S+)-\d+\s+\d+\s+` +
		`([\d.]+)\s+ns/op` +
		`(?:\s+[\d.]+\s+MB/s)?` +
		`(?:\s+([\d.]+)\s+B/op)?` +
		`(?:\s+(\d+)\s+allocs/op)?`,
)

// metaLineRe matches goos/goarch/cpu lines.
var metaLineRe = regexp.MustCompile(`^(goos|goarch|cpu|pkg):\s+(.+)`)

// splitBenchName splits "Benchmark_Marshal_Tiny_StdJSON" into ("Marshal_Tiny", "StdJSON").
// It tries known library suffixes first, then falls back to the last "_"-separated segment.
func splitBenchName(name string) (group, library string) {
	// Strip "Benchmark_" prefix
	trimmed := strings.TrimPrefix(name, "Benchmark_")

	// Try known suffixes
	for _, lib := range knownLibNames() {
		suffix := "_" + lib
		if before, ok := strings.CutSuffix(trimmed, suffix); ok {
			return before, lib
		}
	}

	// Fallback: last segment
	idx := strings.LastIndex(trimmed, "_")
	if idx > 0 {
		return trimmed[:idx], trimmed[idx+1:]
	}
	return trimmed, "Unknown"
}

// ParseBenchOutput reads Go benchmark output from r and returns structured data.
func ParseBenchOutput(r io.Reader) (*BenchData, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var results []BenchResult
	meta := make(map[string]string)

	for scanner.Scan() {
		line := scanner.Text()

		// Check for metadata lines
		if m := metaLineRe.FindStringSubmatch(line); m != nil {
			meta[m[1]] = m[2]
			continue
		}

		// Check for benchmark result lines
		m := benchLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		fullName := m[1]
		nsOp, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			continue
		}

		var bOp, allocsOp float64
		if m[3] != "" {
			bOp, _ = strconv.ParseFloat(m[3], 64)
		}
		if m[4] != "" {
			allocsOp, _ = strconv.ParseFloat(m[4], 64)
		}

		group, library := splitBenchName(fullName)
		results = append(results, BenchResult{
			Name:     fullName,
			Group:    group,
			Library:  library,
			NsOp:     nsOp,
			BOp:      bOp,
			AllocsOp: allocsOp,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading input: %w", err)
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no benchmark results found in input")
	}

	return aggregateResults(results, meta), nil
}

// aggregateResults groups raw results by (group, library), takes medians, and builds BenchData.
func aggregateResults(results []BenchResult, meta map[string]string) *BenchData {
	type key struct{ group, library string }

	// Collect all measurements per (group, library)
	measurements := make(map[key][]BenchResult)
	groupOrder := make(map[string]int)
	libSet := make(map[string]bool)
	orderIdx := 0

	for _, r := range results {
		k := key{r.Group, r.Library}
		measurements[k] = append(measurements[k], r)
		if _, exists := groupOrder[r.Group]; !exists {
			groupOrder[r.Group] = orderIdx
			orderIdx++
		}
		libSet[r.Library] = true
	}

	// Order groups by first appearance
	groups := make([]string, len(groupOrder))
	for g, idx := range groupOrder {
		groups[idx] = g
	}

	// Order libraries: known ones first (in defined order), then unknown alphabetically
	var libs []string
	for _, kl := range knownLibNames() {
		if libSet[kl] {
			libs = append(libs, kl)
			delete(libSet, kl)
		}
	}
	var unknowns []string
	for lib := range libSet {
		unknowns = append(unknowns, lib)
	}
	sort.Strings(unknowns)
	libs = append(libs, unknowns...)

	// Compute medians
	aggResults := make(map[string]map[string]*GroupResult)
	for k, ms := range measurements {
		if _, ok := aggResults[k.group]; !ok {
			aggResults[k.group] = make(map[string]*GroupResult)
		}
		aggResults[k.group][k.library] = &GroupResult{
			Group:    k.group,
			Library:  k.library,
			NsOp:     medianFloat(ms, func(r BenchResult) float64 { return r.NsOp }),
			BOp:      medianFloat(ms, func(r BenchResult) float64 { return r.BOp }),
			AllocsOp: medianFloat(ms, func(r BenchResult) float64 { return r.AllocsOp }),
		}
	}

	// Build subtitle from metadata
	var subtitleParts []string
	for _, key := range []string{"goos", "goarch", "cpu"} {
		if v, ok := meta[key]; ok {
			subtitleParts = append(subtitleParts, key+": "+v)
		}
	}

	// Determine run count from measurements
	runCount := 0
	for _, ms := range measurements {
		if len(ms) > runCount {
			runCount = len(ms)
		}
	}

	// Build sections from groups (preserving order)
	var sections []Section
	sectionIdx := make(map[string]int) // section name -> index in sections slice
	for _, g := range groups {
		sec, _ := splitGroupToSection(g)
		if idx, ok := sectionIdx[sec]; ok {
			sections[idx].Groups = append(sections[idx].Groups, g)
		} else {
			sectionIdx[sec] = len(sections)
			sections = append(sections, Section{Name: sec, Groups: []string{g}})
		}
	}

	return &BenchData{
		Subtitle: strings.Join(subtitleParts, "  |  "),
		Groups:   groups,
		Sections: sections,
		Libs:     libs,
		Results:  aggResults,
		RunCount: runCount,
	}
}

// medianFloat computes the median of a float64 field from a slice.
func medianFloat(items []BenchResult, extract func(BenchResult) float64) float64 {
	vals := make([]float64, len(items))
	for i, item := range items {
		vals[i] = extract(item)
	}
	sort.Float64s(vals)
	n := len(vals)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return vals[n/2]
	}
	return (vals[n/2-1] + vals[n/2]) / 2
}

// FormatNsOp formats nanoseconds with appropriate units.
func FormatNsOp(ns float64) string {
	switch {
	case ns >= 1e9:
		return fmt.Sprintf("%.2fs", ns/1e9)
	case ns >= 1e6:
		return fmt.Sprintf("%.1fms", ns/1e6)
	case ns >= 1e3:
		return fmt.Sprintf("%.1f\u00b5s", ns/1e3)
	default:
		return fmt.Sprintf("%.1fns", ns)
	}
}

// FormatBytes formats byte counts with appropriate units.
func FormatBytes(b float64) string {
	switch {
	case b >= 1024*1024:
		return fmt.Sprintf("%.1fMB", b/(1024*1024))
	case b >= 1024:
		return fmt.Sprintf("%.1fKB", b/1024)
	default:
		return fmt.Sprintf("%.0fB", b)
	}
}

// FormatAllocs formats allocation count.
func FormatAllocs(a float64) string {
	if a == math.Trunc(a) {
		return fmt.Sprintf("%d", int64(a))
	}
	return fmt.Sprintf("%.1f", a)
}
