package perfbench

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const GoBenchmarkSchemaVersion = 1

var goBenchmarkLine = regexp.MustCompile(`^(Benchmark\S+)-\d+\s+\d+\s+([0-9.eE+-]+)\s+ns/op(?:\s+[0-9.eE+-]+\s+MB/s)?\s+([0-9.eE+-]+)\s+B/op\s+([0-9.eE+-]+)\s+allocs/op\s*$`)

type GoBenchmarkMetrics struct {
	NSPerOp     float64 `json:"ns_per_op"`
	BytesPerOp  float64 `json:"bytes_per_op"`
	AllocsPerOp float64 `json:"allocs_per_op"`
}

type GoBenchmarkSuite struct {
	SchemaVersion int                           `json:"schema_version"`
	Metadata      Metadata                      `json:"metadata"`
	Benchmarks    map[string]GoBenchmarkMetrics `json:"benchmarks"`
}

type GoBenchmarkBudget struct {
	MaximumNSRegressionPct     float64 `json:"maximum_ns_regression_percent"`
	MaximumBytesRegressionPct  float64 `json:"maximum_bytes_regression_percent"`
	MaximumAllocsRegressionPct float64 `json:"maximum_allocs_regression_percent"`
}

type GoBenchmarkCheck struct {
	Benchmark string  `json:"benchmark"`
	Metric    string  `json:"metric"`
	Passed    bool    `json:"passed"`
	Baseline  float64 `json:"baseline"`
	Current   float64 `json:"current"`
	Limit     float64 `json:"limit"`
}

type GoBenchmarkReport struct {
	Passed bool               `json:"passed"`
	Checks []GoBenchmarkCheck `json:"checks"`
	Error  string             `json:"error,omitempty"`
}

func ParseGoBenchmarks(reader io.Reader) (GoBenchmarkSuite, error) {
	type samples struct {
		ns     []float64
		bytes  []float64
		allocs []float64
	}
	collected := make(map[string]*samples)
	metadata := runtimeMetadata()
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "goos: "):
			metadata.OS = strings.TrimSpace(strings.TrimPrefix(line, "goos: "))
		case strings.HasPrefix(line, "goarch: "):
			metadata.Arch = strings.TrimSpace(strings.TrimPrefix(line, "goarch: "))
		case strings.HasPrefix(line, "cpu: "):
			metadata.CPUModel = strings.TrimSpace(strings.TrimPrefix(line, "cpu: "))
		}
		matches := goBenchmarkLine.FindStringSubmatch(line)
		if matches == nil {
			continue
		}
		values := make([]float64, 3)
		for index := range values {
			value, err := strconv.ParseFloat(matches[index+2], 64)
			if err != nil {
				return GoBenchmarkSuite{}, fmt.Errorf("parse benchmark metric %q: %w", matches[index+2], err)
			}
			values[index] = value
		}
		entry := collected[matches[1]]
		if entry == nil {
			entry = &samples{}
			collected[matches[1]] = entry
		}
		entry.ns = append(entry.ns, values[0])
		entry.bytes = append(entry.bytes, values[1])
		entry.allocs = append(entry.allocs, values[2])
	}
	if err := scanner.Err(); err != nil {
		return GoBenchmarkSuite{}, err
	}
	if len(collected) == 0 {
		return GoBenchmarkSuite{}, fmt.Errorf("no Go benchmark results found")
	}
	benchmarks := make(map[string]GoBenchmarkMetrics, len(collected))
	for name, values := range collected {
		benchmarks[name] = GoBenchmarkMetrics{NSPerOp: median(values.ns), BytesPerOp: median(values.bytes), AllocsPerOp: median(values.allocs)}
	}
	return GoBenchmarkSuite{SchemaVersion: GoBenchmarkSchemaVersion, Metadata: metadata, Benchmarks: benchmarks}, nil
}

func CheckGoBenchmarkBudget(baseline, current GoBenchmarkSuite, budget GoBenchmarkBudget) GoBenchmarkReport {
	if baseline.SchemaVersion != current.SchemaVersion {
		return GoBenchmarkReport{Error: fmt.Sprintf("schema version differs: baseline %d, current %d", baseline.SchemaVersion, current.SchemaVersion)}
	}
	if err := compatibleMetadata(baseline.Metadata, current.Metadata); err != nil {
		return GoBenchmarkReport{Error: err.Error()}
	}
	if len(baseline.Benchmarks) != len(current.Benchmarks) {
		return GoBenchmarkReport{Error: fmt.Sprintf("benchmark set differs: baseline has %d, current has %d", len(baseline.Benchmarks), len(current.Benchmarks))}
	}
	names := make([]string, 0, len(baseline.Benchmarks))
	for name := range baseline.Benchmarks {
		if _, ok := current.Benchmarks[name]; !ok {
			return GoBenchmarkReport{Error: fmt.Sprintf("current results are missing %s", name)}
		}
		names = append(names, name)
	}
	sort.Strings(names)
	report := GoBenchmarkReport{Passed: true}
	for _, name := range names {
		base, now := baseline.Benchmarks[name], current.Benchmarks[name]
		checks := []GoBenchmarkCheck{
			goMaximumCheck(name, "ns/op", base.NSPerOp, now.NSPerOp, budget.MaximumNSRegressionPct),
			goMaximumCheck(name, "B/op", base.BytesPerOp, now.BytesPerOp, budget.MaximumBytesRegressionPct),
			goMaximumCheck(name, "allocs/op", base.AllocsPerOp, now.AllocsPerOp, budget.MaximumAllocsRegressionPct),
		}
		for _, check := range checks {
			if !check.Passed {
				report.Passed = false
			}
			report.Checks = append(report.Checks, check)
		}
	}
	return report
}

func compatibleMetadata(baseline, current Metadata) error {
	checks := []struct {
		name string
		a    any
		b    any
	}{
		{"Go version", baseline.GoVersion, current.GoVersion}, {"operating system", baseline.OS, current.OS},
		{"architecture", baseline.Arch, current.Arch}, {"CPU count", baseline.CPUCount, current.CPUCount},
		{"CPU model", baseline.CPUModel, current.CPUModel}, {"system memory", baseline.MemoryBytes, current.MemoryBytes},
	}
	for _, check := range checks {
		if fmt.Sprint(check.a) != fmt.Sprint(check.b) {
			return fmt.Errorf("incompatible runtime profiles: %s differs (baseline %v, current %v)", check.name, check.a, check.b)
		}
	}
	return nil
}

func goMaximumCheck(name, metric string, baseline, current, tolerancePercent float64) GoBenchmarkCheck {
	limit := baseline * (1 + tolerancePercent/100)
	return GoBenchmarkCheck{Benchmark: name, Metric: metric, Passed: current <= limit, Baseline: baseline, Current: current, Limit: limit}
}

func median(values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle]
	}
	return (sorted[middle-1] + sorted[middle]) / 2
}
