package perfbench

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

func WriteJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func ReadResult(reader io.Reader) (Result, error) {
	var result Result
	if err := json.NewDecoder(reader).Decode(&result); err != nil {
		return Result{}, err
	}
	if result.SchemaVersion != SchemaVersion {
		return Result{}, fmt.Errorf("unsupported result schema version %d", result.SchemaVersion)
	}
	return result, nil
}

func Markdown(result Result) string {
	config := result.Config
	load := fmt.Sprintf("concurrency=%d", config.Concurrency)
	if config.Mode == ModeRate {
		load = fmt.Sprintf("rate=%d rps", config.Rate)
	}
	var output strings.Builder
	fmt.Fprintf(&output, "# Performance benchmark: %s\n\n", config.Label)
	dirty := ""
	if result.Metadata.GitDirty {
		dirty = " (dirty working tree)"
	}
	fmt.Fprintf(&output, "- Generated: %s\n- Commit: `%s`%s\n- Runtime: `%s %s/%s`, %d CPUs", result.Metadata.GeneratedAt.Format("2006-01-02T15:04:05Z"), result.Metadata.GitCommit, dirty, result.Metadata.GoVersion, result.Metadata.OS, result.Metadata.Arch, result.Metadata.CPUCount)
	if result.Metadata.CPUModel != "" {
		fmt.Fprintf(&output, ", %s", result.Metadata.CPUModel)
	}
	if result.Metadata.MemoryBytes > 0 {
		fmt.Fprintf(&output, ", %.1f GiB RAM", float64(result.Metadata.MemoryBytes)/(1<<30))
	}
	output.WriteString("\n")
	fmt.Fprintf(&output, "- Scenario: `%s`, stream=%t, %s, duration=%s, request=%d bytes\n\n", config.Protocol, config.Stream, load, config.Duration, config.RequestBytes)
	output.WriteString("| Metric | Value |\n| --- | ---: |\n")
	fmt.Fprintf(&output, "| Requests | %d |\n| Success rate | %.3f%% |\n| Achieved throughput | %.2f requests/s |\n", result.Summary.Requests, result.Summary.SuccessRatePercent, result.Summary.AchievedRPS)
	if result.Summary.DroppedRequests > 0 {
		fmt.Fprintf(&output, "| Offered / generator-dropped | %d / %d |\n", result.Summary.OfferedRequests, result.Summary.DroppedRequests)
	}
	fmt.Fprintf(&output, "| Latency P50 / P95 / P99 | %.3f / %.3f / %.3f ms |\n", result.Summary.LatencyMS.P50, result.Summary.LatencyMS.P95, result.Summary.LatencyMS.P99)
	if config.Stream {
		fmt.Fprintf(&output, "| TTFT P50 / P95 / P99 | %.3f / %.3f / %.3f ms |\n", result.Summary.TTFTMS.P50, result.Summary.TTFTMS.P95, result.Summary.TTFTMS.P99)
		fmt.Fprintf(&output, "| Estimated gateway TTFT P50 / P95 / P99 | %.3f / %.3f / %.3f ms |\n", result.Summary.EstimatedGatewayTTFTMS.P50, result.Summary.EstimatedGatewayTTFTMS.P95, result.Summary.EstimatedGatewayTTFTMS.P99)
	}
	fmt.Fprintf(&output, "| Estimated gateway overhead P50 / P95 / P99 | %.3f / %.3f / %.3f ms |\n", result.Summary.EstimatedGatewayOverheadMS.P50, result.Summary.EstimatedGatewayOverheadMS.P95, result.Summary.EstimatedGatewayOverheadMS.P99)
	output.WriteString("\nEstimated gateway overhead is end-to-end client latency minus configured fake-upstream latency, clamped at zero. It is an estimate, not an internal timer.\n")
	if len(result.Summary.DropReasons) > 0 {
		keys := make([]string, 0, len(result.Summary.DropReasons))
		for key := range result.Summary.DropReasons {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		output.WriteString("\nFailures: ")
		for index, key := range keys {
			if index > 0 {
				output.WriteString(", ")
			}
			fmt.Fprintf(&output, "%s=%d", key, result.Summary.DropReasons[key])
		}
		output.WriteString(".\n")
	}
	return output.String()
}

func BudgetMarkdown(report BudgetReport) string {
	if report.Error != "" {
		return "# Performance budget: FAIL\n\n" + report.Error + "\n"
	}
	status := "PASS"
	if !report.Passed {
		status = "FAIL"
	}
	var output strings.Builder
	fmt.Fprintf(&output, "# Performance budget: %s\n\n| Check | Result | Detail |\n| --- | --- | --- |\n", status)
	for _, check := range report.Checks {
		result := "PASS"
		if !check.Passed {
			result = "FAIL"
		}
		fmt.Fprintf(&output, "| %s | %s | %s |\n", check.Name, result, check.Detail)
	}
	return output.String()
}
