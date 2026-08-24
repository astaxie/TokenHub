package perfbench

import "fmt"

type Budget struct {
	MinimumSuccessRatePercent float64 `json:"minimum_success_rate_percent"`
	MinimumThroughputRatio    float64 `json:"minimum_throughput_ratio"`
	MaximumMeanRegressionPct  float64 `json:"maximum_mean_regression_percent"`
	MaximumP99RegressionPct   float64 `json:"maximum_p99_regression_percent"`
}

type BudgetCheck struct {
	Name     string  `json:"name"`
	Passed   bool    `json:"passed"`
	Baseline float64 `json:"baseline,omitempty"`
	Current  float64 `json:"current"`
	Limit    float64 `json:"limit"`
	Detail   string  `json:"detail"`
}

type BudgetReport struct {
	Passed bool          `json:"passed"`
	Checks []BudgetCheck `json:"checks"`
	Error  string        `json:"error,omitempty"`
}

func CheckBudget(baseline Result, current Result, budget Budget) BudgetReport {
	if err := compatibleScenarios(baseline, current); err != nil {
		return BudgetReport{Passed: false, Error: err.Error()}
	}
	checks := []BudgetCheck{
		minimumCheck("success_rate", current.Summary.SuccessRatePercent, budget.MinimumSuccessRatePercent, "percent"),
		minimumCheck("throughput", current.Summary.AchievedRPS, baseline.Summary.AchievedRPS*budget.MinimumThroughputRatio, "requests/second"),
		maximumRegressionCheck("mean_latency", baseline.Summary.LatencyMS.Mean, current.Summary.LatencyMS.Mean, budget.MaximumMeanRegressionPct),
		maximumRegressionCheck("p99_latency", baseline.Summary.LatencyMS.P99, current.Summary.LatencyMS.P99, budget.MaximumP99RegressionPct),
	}
	checks[1].Baseline = baseline.Summary.AchievedRPS
	report := BudgetReport{Passed: true, Checks: checks}
	for _, check := range checks {
		if !check.Passed {
			report.Passed = false
		}
	}
	if current.Summary.DroppedRequests > 0 {
		report.Passed = false
		report.Checks = append(report.Checks, BudgetCheck{Name: "load_generator_drops", Passed: false, Current: float64(current.Summary.DroppedRequests), Limit: 0, Detail: fmt.Sprintf("load generator dropped %d offered requests", current.Summary.DroppedRequests)})
	}
	return report
}

func compatibleScenarios(baseline, current Result) error {
	if baseline.SchemaVersion != current.SchemaVersion {
		return fmt.Errorf("schema version differs: baseline %d, current %d", baseline.SchemaVersion, current.SchemaVersion)
	}
	a, b := baseline.Config, current.Config
	checks := []struct {
		name string
		a    any
		b    any
	}{
		{"protocol", a.Protocol, b.Protocol}, {"stream", a.Stream, b.Stream}, {"mode", a.Mode, b.Mode},
		{"rate", a.Rate, b.Rate}, {"concurrency", a.Concurrency, b.Concurrency}, {"model", a.Model, b.Model},
		{"request bytes", a.RequestBytes, b.RequestBytes}, {"expected upstream latency", a.ExpectedUpstreamLatency, b.ExpectedUpstreamLatency},
		{"response bytes", a.ResponseBytes, b.ResponseBytes}, {"stream chunks", a.StreamChunks, b.StreamChunks}, {"chunk interval", a.ChunkInterval, b.ChunkInterval},
		{"expected upstream TTFT", a.ExpectedUpstreamTTFT, b.ExpectedUpstreamTTFT}, {"deployment profile", a.DeploymentProfile, b.DeploymentProfile},
		{"duration", a.Duration, b.Duration}, {"warmup", a.Warmup, b.Warmup}, {"timeout", a.Timeout, b.Timeout}, {"max in flight", a.MaxInFlight, b.MaxInFlight},
	}
	for _, check := range checks {
		if fmt.Sprint(check.a) != fmt.Sprint(check.b) {
			return fmt.Errorf("incompatible scenarios: %s differs (baseline %v, current %v)", check.name, check.a, check.b)
		}
	}
	if err := compatibleMetadata(baseline.Metadata, current.Metadata); err != nil {
		return err
	}
	return nil
}

func minimumCheck(name string, current float64, minimum float64, unit string) BudgetCheck {
	return BudgetCheck{
		Name:    name,
		Passed:  current >= minimum,
		Current: current,
		Limit:   minimum,
		Detail:  fmt.Sprintf("%.3f %s must be at least %.3f", current, unit, minimum),
	}
}

func maximumRegressionCheck(name string, baseline float64, current float64, tolerancePercent float64) BudgetCheck {
	limit := baseline * (1 + tolerancePercent/100)
	return BudgetCheck{
		Name:     name,
		Passed:   current <= limit,
		Baseline: baseline,
		Current:  current,
		Limit:    limit,
		Detail:   fmt.Sprintf("%.3f ms must not exceed %.3f ms (baseline %.3f ms + %.1f%%)", current, limit, baseline, tolerancePercent),
	}
}
