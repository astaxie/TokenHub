package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHelpListsBenchmarkCommands(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	if err := run(t.Context(), []string{"help"}, &output, &output); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"mocker", "gateway", "run", "check", "summarize-go", "check-go"} {
		if !strings.Contains(output.String(), command) {
			t.Fatalf("help does not list %q: %s", command, output.String())
		}
	}
}

func TestSummarizeAndCheckGoBenchmarks(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	rawPath := filepath.Join(directory, "benchmarks.txt")
	baselinePath := filepath.Join(directory, "baseline.json")
	budgetPath := filepath.Join(directory, "budget.json")
	raw := "goos: " + runtime.GOOS + "\ngoarch: " + runtime.GOARCH + "\ncpu: Test CPU\nBenchmarkGateway/SQLite-8  10  1000 ns/op  200 B/op  10 allocs/op\n"
	if err := os.WriteFile(rawPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run(t.Context(), []string{"summarize-go", "--input", rawPath, "--output", baselinePath, "--git-commit", "test-commit"}, &output, &output); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(budgetPath, []byte(`{"maximum_ns_regression_percent":25,"maximum_bytes_regression_percent":15,"maximum_allocs_regression_percent":10}`), 0o600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := run(t.Context(), []string{"check-go", "--baseline", baselinePath, "--current", rawPath, "--budget", budgetPath}, &output, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"passed": true`) {
		t.Fatalf("unexpected check report: %s", output.String())
	}
}

func TestGatewayRequiresBenchmarkKeyFromEnvironment(t *testing.T) {
	t.Setenv("TOKENHUB_BENCHMARK_API_KEY", "")

	var output bytes.Buffer
	err := run(t.Context(), []string{"gateway"}, &output, &output)
	if err == nil || !strings.Contains(err.Error(), "TOKENHUB_BENCHMARK_API_KEY") {
		t.Fatalf("expected missing benchmark key error, got %v", err)
	}
}
