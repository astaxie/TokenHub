package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"tokenhub/backend/internal/perfbench"
	"tokenhub/backend/internal/server"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		usage(stderr)
		return errors.New("a command is required")
	}
	switch args[0] {
	case "mocker":
		return runMocker(args[1:], stderr)
	case "gateway":
		return runGateway(args[1:], stderr)
	case "run":
		return runBenchmark(ctx, args[1:], stdout, stderr)
	case "check":
		return runCheck(args[1:], stdout, stderr)
	case "summarize-go":
		return runSummarizeGo(args[1:], stdout, stderr)
	case "check-go":
		return runCheckGo(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return nil
	default:
		usage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage(writer io.Writer) {
	_, _ = fmt.Fprintln(writer, "usage: tokenhub-benchmark <mocker|gateway|run|check|summarize-go|check-go> [flags]")
}

func runMocker(args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("mocker", flag.ContinueOnError)
	flags.SetOutput(stderr)
	listen := flags.String("listen", "127.0.0.1:18081", "listen address")
	latency := flags.Duration("latency", 5*time.Millisecond, "response latency")
	responseBytes := flags.Int("response-bytes", 1024, "approximate response bytes")
	streamChunks := flags.Int("stream-chunks", 8, "stream content chunks")
	chunkInterval := flags.Duration("chunk-interval", time.Millisecond, "delay between stream chunks")
	failureEvery := flags.Int("failure-every", 0, "return a failure every N requests (zero disables)")
	failureStatus := flags.Int("failure-status", http.StatusServiceUnavailable, "injected HTTP failure status")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *failureEvery < 0 {
		return errors.New("--failure-every cannot be negative")
	}
	handler := perfbench.NewMockHandler(perfbench.MockConfig{Latency: *latency, ResponseBytes: *responseBytes, StreamChunks: *streamChunks, ChunkInterval: *chunkInterval, FailureEvery: uint64(*failureEvery), FailureStatus: *failureStatus})
	server := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	_, _ = fmt.Fprintf(stderr, "deterministic upstream listening on http://%s\n", *listen)
	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func runGateway(args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("gateway", flag.ContinueOnError)
	flags.SetOutput(stderr)
	listen := flags.String("listen", "127.0.0.1:18080", "listen address")
	model := flags.String("model", "benchmark-model", "model name")
	latency := flags.Duration("upstream-latency", 5*time.Millisecond, "deterministic upstream response latency")
	responseBytes := flags.Int("response-bytes", 1024, "approximate upstream response bytes")
	streamChunks := flags.Int("stream-chunks", 8, "stream content chunks")
	chunkInterval := flags.Duration("chunk-interval", time.Millisecond, "delay between stream chunks")
	failover := flags.Bool("failover", false, "add an always-failing primary before the healthy upstream")
	if err := flags.Parse(args); err != nil {
		return err
	}
	apiKeyValue, _ := os.LookupEnv("TOKENHUB_BENCHMARK_API_KEY")
	apiKey := strings.TrimSpace(apiKeyValue)
	if apiKey == "" {
		return errors.New("TOKENHUB_BENCHMARK_API_KEY is required")
	}
	config := server.Config{
		AdminToken:               "benchmark-local-admin",
		Environment:              "benchmark",
		SecretKey:                "benchmark-local-secret-key-at-least-32-bytes",
		DBMaxOpenConns:           32,
		DBMaxIdleConns:           32,
		GracefulShutdownSeconds:  2,
		ResourceFailureThreshold: 1_000_000_000,
	}
	store := server.NewMemoryStoreWithConfig(config)
	project := store.CreateProject(server.Project{Name: "Performance benchmark", Status: server.StatusActive})
	if _, _, err := store.CreateAPIKey(project.ID, server.APIKey{Name: "benchmark-key", Allowed: []string{*model}, Status: server.StatusActive}, apiKey); err != nil {
		return err
	}
	store.AddModel(server.Model{Name: *model, Modality: "chat", Status: server.StatusActive})
	upstreams := make([]*httptest.Server, 0, 2)
	if *failover {
		upstreams = append(upstreams, httptest.NewServer(perfbench.NewMockHandler(perfbench.MockConfig{Latency: *latency, FailureEvery: 1, FailureStatus: http.StatusServiceUnavailable})))
	}
	upstreams = append(upstreams, httptest.NewServer(perfbench.NewMockHandler(perfbench.MockConfig{Latency: *latency, ResponseBytes: *responseBytes, StreamChunks: *streamChunks, ChunkInterval: *chunkInterval})))
	defer func() {
		for _, upstream := range upstreams {
			upstream.Close()
		}
	}()
	for index, upstream := range upstreams {
		provider := store.AddProvider(server.Provider{Name: fmt.Sprintf("Deterministic benchmark upstream %d", index+1), Type: server.ProviderOpenAICompatible, BaseURL: upstream.URL, Status: server.StatusActive, Healthy: true})
		resource, addErr := store.AddProviderResource(server.ProviderResource{ProviderID: provider.ID, Name: fmt.Sprintf("Benchmark resource %d", index+1), ResourceType: "openai", Status: server.StatusActive, Healthy: true, Priority: 1, Weight: 100, MaxConcurrency: 10000})
		if addErr != nil {
			return addErr
		}
		store.AddRoute(server.ModelRoute{ModelName: *model, ProviderID: provider.ID, ProviderResourceID: resource.ID, ProviderModel: *model, Priority: index + 1, Weight: 100, Status: server.StatusActive, Strategy: server.RouteStrategyPriorityOnly})
	}
	app := server.NewWithConfig(store, config)
	defer func() { _ = app.Shutdown(context.Background()) }()
	_, _ = fmt.Fprintf(stderr, "self-contained TokenHub benchmark gateway listening on http://%s\n", *listen)
	httpServer := &http.Server{Addr: *listen, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second}
	err := httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func runBenchmark(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	config := perfbench.Config{}
	flags.StringVar(&config.Label, "label", "tokenhub", "result label")
	flags.StringVar(&config.BaseURL, "base-url", "http://127.0.0.1:8080", "OpenAI-compatible gateway base URL")
	flags.StringVar(&config.Model, "model", "benchmark-model", "model name")
	protocol := flags.String("protocol", string(perfbench.ProtocolChat), "chat, responses, or embedding")
	mode := flags.String("mode", string(perfbench.ModeConcurrency), "concurrency or rate")
	flags.BoolVar(&config.Stream, "stream", false, "request streaming responses")
	flags.IntVar(&config.Rate, "rate", 0, "fixed requests per second")
	flags.IntVar(&config.Concurrency, "concurrency", 8, "fixed worker count")
	flags.IntVar(&config.MaxInFlight, "max-in-flight", 1000, "maximum outstanding fixed-rate requests")
	flags.DurationVar(&config.Duration, "duration", 10*time.Second, "measured duration")
	flags.DurationVar(&config.Warmup, "warmup", 2*time.Second, "unmeasured warmup")
	flags.DurationVar(&config.Timeout, "timeout", 30*time.Second, "per-request timeout")
	flags.IntVar(&config.RequestBytes, "request-bytes", 256, "minimum unique prompt bytes")
	flags.IntVar(&config.ResponseBytes, "response-bytes", 1024, "configured fake-upstream response bytes")
	flags.IntVar(&config.StreamChunks, "stream-chunks", 8, "configured fake-upstream stream chunks")
	flags.DurationVar(&config.ChunkInterval, "chunk-interval", time.Millisecond, "configured fake-upstream chunk interval")
	flags.StringVar(&config.DeploymentProfile, "deployment-profile", "unspecified", "database and telemetry profile shared by compared targets")
	flags.DurationVar(&config.ExpectedUpstreamLatency, "upstream-latency", 5*time.Millisecond, "expected total upstream response duration")
	upstreamTTFT := flags.Duration("upstream-ttft", -1, "expected upstream time to first byte (defaults to upstream latency)")
	apiKeyEnv := flags.String("api-key-env", "TOKENHUB_BENCHMARK_API_KEY", "environment variable containing the gateway API key")
	jsonPath := flags.String("json", "", "JSON result path (stdout when empty)")
	markdownPath := flags.String("markdown", "", "optional Markdown result path")
	gitCommit := flags.String("git-commit", "", "commit under test (auto-detected when empty)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	config.Protocol = perfbench.Protocol(*protocol)
	config.Mode = perfbench.LoadMode(*mode)
	if *upstreamTTFT < 0 {
		config.ExpectedUpstreamTTFT = config.ExpectedUpstreamLatency
	} else {
		config.ExpectedUpstreamTTFT = *upstreamTTFT
	}
	if config.Mode == perfbench.ModeRate && !flagWasSet(flags, "concurrency") {
		config.Concurrency = 0
	}
	if *apiKeyEnv != "" {
		config.APIKey = os.Getenv(*apiKeyEnv)
	}
	result, err := perfbench.Run(ctx, config)
	if err != nil {
		return err
	}
	result.Metadata.GitCommit = strings.TrimSpace(*gitCommit)
	if result.Metadata.GitCommit == "" {
		result.Metadata.GitCommit = detectGitCommit()
	}
	result.Metadata.GitDirty = detectGitDirty()
	if *jsonPath == "" {
		if err := perfbench.WriteJSON(stdout, result); err != nil {
			return err
		}
	} else if err := writeFile(*jsonPath, func(writer io.Writer) error { return perfbench.WriteJSON(writer, result) }); err != nil {
		return err
	}
	if *markdownPath != "" {
		if err := writeFile(*markdownPath, func(writer io.Writer) error {
			_, err := io.WriteString(writer, perfbench.Markdown(result))
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

func runCheck(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	baselinePath := flags.String("baseline", "", "baseline JSON result")
	currentPath := flags.String("current", "", "current JSON result")
	budgetPath := flags.String("budget", "", "performance budget JSON")
	reportPath := flags.String("markdown", "", "optional Markdown check report")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *baselinePath == "" || *currentPath == "" || *budgetPath == "" {
		return errors.New("--baseline, --current, and --budget are required")
	}
	baseline, err := readResultFile(*baselinePath)
	if err != nil {
		return fmt.Errorf("read baseline: %w", err)
	}
	current, err := readResultFile(*currentPath)
	if err != nil {
		return fmt.Errorf("read current result: %w", err)
	}
	var budget perfbench.Budget
	if err := readJSONFile(*budgetPath, &budget); err != nil {
		return fmt.Errorf("read budget: %w", err)
	}
	report := perfbench.CheckBudget(baseline, current, budget)
	if err := perfbench.WriteJSON(stdout, report); err != nil {
		return err
	}
	if *reportPath != "" {
		if err := writeFile(*reportPath, func(writer io.Writer) error {
			_, err := io.WriteString(writer, perfbench.BudgetMarkdown(report))
			return err
		}); err != nil {
			return err
		}
	}
	if !report.Passed {
		return errors.New("performance budget failed")
	}
	return nil
}

func runSummarizeGo(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("summarize-go", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inputPath := flags.String("input", "", "raw go test benchmark output")
	outputPath := flags.String("output", "", "summary JSON path (stdout when empty)")
	gitCommit := flags.String("git-commit", "", "commit under test (auto-detected when empty)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *inputPath == "" {
		return errors.New("--input is required")
	}
	input, err := os.Open(*inputPath)
	if err != nil {
		return fmt.Errorf("open Go benchmark output: %w", err)
	}
	suite, parseErr := perfbench.ParseGoBenchmarks(input)
	closeErr := input.Close()
	if parseErr != nil {
		return parseErr
	}
	if closeErr != nil {
		return closeErr
	}
	suite.Metadata.GitCommit = strings.TrimSpace(*gitCommit)
	if suite.Metadata.GitCommit == "" {
		suite.Metadata.GitCommit = detectGitCommit()
	}
	suite.Metadata.GitDirty = detectGitDirty()
	write := func(writer io.Writer) error {
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		return encoder.Encode(suite)
	}
	if *outputPath == "" {
		return write(stdout)
	}
	return writeFile(*outputPath, write)
}

func runCheckGo(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("check-go", flag.ContinueOnError)
	flags.SetOutput(stderr)
	baselinePath := flags.String("baseline", "", "Go benchmark baseline JSON")
	currentPath := flags.String("current", "", "raw current go test benchmark output")
	budgetPath := flags.String("budget", "", "Go benchmark budget JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *baselinePath == "" || *currentPath == "" || *budgetPath == "" {
		return errors.New("--baseline, --current, and --budget are required")
	}
	var baseline perfbench.GoBenchmarkSuite
	if err := readJSONFile(*baselinePath, &baseline); err != nil {
		return fmt.Errorf("read Go benchmark baseline: %w", err)
	}
	currentFile, err := os.Open(*currentPath)
	if err != nil {
		return fmt.Errorf("open current Go benchmark output: %w", err)
	}
	current, parseErr := perfbench.ParseGoBenchmarks(currentFile)
	closeErr := currentFile.Close()
	if parseErr != nil {
		return parseErr
	}
	if closeErr != nil {
		return closeErr
	}
	var budget perfbench.GoBenchmarkBudget
	if err := readJSONFile(*budgetPath, &budget); err != nil {
		return fmt.Errorf("read Go benchmark budget: %w", err)
	}
	report := perfbench.CheckGoBenchmarkBudget(baseline, current, budget)
	if err := perfbench.WriteJSON(stdout, report); err != nil {
		return err
	}
	if !report.Passed {
		return errors.New("go benchmark performance budget failed")
	}
	return nil
}

func flagWasSet(flags *flag.FlagSet, name string) bool {
	set := false
	flags.Visit(func(flag *flag.Flag) {
		if flag.Name == name {
			set = true
		}
	})
	return set
}

func detectGitCommit() string {
	output, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(output))
}

func detectGitDirty() bool {
	output, err := exec.Command("git", "status", "--porcelain", "--untracked-files=normal").Output()
	return err == nil && len(bytes.TrimSpace(output)) > 0
}

func readResultFile(path string) (perfbench.Result, error) {
	file, err := os.Open(path)
	if err != nil {
		return perfbench.Result{}, err
	}
	defer file.Close()
	return perfbench.ReadResult(file)
}

func readJSONFile(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func writeFile(path string, write func(io.Writer) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := write(file); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}
