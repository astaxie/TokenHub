package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type backgroundFixture struct {
	SchemaVersion int              `json:"schema_version"`
	Protocol      string           `json:"protocol"`
	Kind          string           `json:"kind"`
	PluginID      string           `json:"plugin_id"`
	JobID         string           `json:"job_id"`
	Schedule      string           `json:"schedule"`
	Cases         []backgroundCase `json:"cases"`
}

type backgroundCase struct {
	Name               string           `json:"name"`
	Trigger            string           `json:"trigger"`
	Actor              map[string]any   `json:"actor"`
	Payload            map[string]any   `json:"payload"`
	ForbiddenFragments []string         `json:"forbidden_fragments"`
	Expect             backgroundExpect `json:"expect"`
}

type backgroundExpect struct {
	ResourceID     string `json:"resource_id"`
	Heartbeat      string `json:"heartbeat"`
	Trigger        string `json:"trigger"`
	ActorID        string `json:"actor_id"`
	Count          int64  `json:"count"`
	MetadataStatus string `json:"metadata_status"`
}

type backgroundInvocation struct {
	PluginID string         `json:"plugin_id"`
	JobID    string         `json:"job_id"`
	Trigger  string         `json:"trigger,omitempty"`
	Actor    map[string]any `json:"actor,omitempty"`
	Payload  map[string]any `json:"payload,omitempty"`
}

type backgroundResult struct {
	Data     map[string]any    `json:"data,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

func runBackground(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("background", flag.ContinueOnError)
	flags.SetOutput(stderr)
	packageDir := flags.String("package", "", "background job plugin package directory")
	fixturePath := flags.String("fixture", "", "background job fixture")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*packageDir) == "" {
		return errors.New("background --package is required")
	}
	manifest, err := readManifest(*packageDir)
	if err != nil {
		return err
	}
	fixture, err := readBackgroundFixture(*fixturePath)
	if err != nil {
		return err
	}
	if err := validateBackgroundManifest(manifest, fixture); err != nil {
		return err
	}
	commandPath, cleanup, err := pluginCommandPath(ctx, *packageDir, manifest.Entry.Backend.Command, "background")
	if err != nil {
		return err
	}
	defer cleanup()
	for _, testCase := range fixture.Cases {
		result, rawOutput, err := executeBackgroundCase(ctx, *packageDir, commandPath, fixture, testCase)
		if err != nil {
			return fmt.Errorf("%s: %w", testCase.Name, err)
		}
		for _, fragment := range testCase.ForbiddenFragments {
			if bytes.Contains(rawOutput, []byte(fragment)) {
				return fmt.Errorf("%s: forbidden fragment %q leaked to stdout", testCase.Name, fragment)
			}
		}
		if err := assertBackgroundResult(testCase, result); err != nil {
			return fmt.Errorf("%s: %w", testCase.Name, err)
		}
		fmt.Fprintf(stdout, "background %s: ok\n", testCase.Name)
	}
	fmt.Fprintf(stdout, "background contract passed (%d cases, manifest %s %s)\n", len(fixture.Cases), manifest.ID, manifest.Version)
	return nil
}

func readBackgroundFixture(path string) (backgroundFixture, error) {
	if strings.TrimSpace(path) == "" {
		defaultPath, err := defaultBackgroundFixturePath()
		if err != nil {
			return backgroundFixture{}, err
		}
		path = defaultPath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return backgroundFixture{}, fmt.Errorf("read background fixture: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var fixture backgroundFixture
	if err := decoder.Decode(&fixture); err != nil {
		return backgroundFixture{}, fmt.Errorf("decode background fixture: %w", err)
	}
	if fixture.SchemaVersion != 1 || fixture.Protocol != "stdio-json-v1" || fixture.Kind != "background_job" {
		return backgroundFixture{}, errors.New("background fixture must be schema_version 1 stdio-json-v1 background_job")
	}
	if fixture.PluginID == "" || fixture.JobID == "" || fixture.Schedule == "" || len(fixture.Cases) == 0 {
		return backgroundFixture{}, errors.New("background fixture must include plugin_id, job_id, schedule, and at least one case")
	}
	return fixture, nil
}

func defaultBackgroundFixturePath() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("cannot resolve default background fixture path")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	return filepath.Join(root, "contract-tests", "protocol", "stdio-json-v1", "background_heartbeat.json"), nil
}

func validateBackgroundManifest(manifest manifest, fixture backgroundFixture) error {
	if manifest.SchemaVersion != 1 {
		return fmt.Errorf("manifest schema_version = %d, want 1", manifest.SchemaVersion)
	}
	if manifest.ID != fixture.PluginID {
		return fmt.Errorf("manifest id = %q, want %q", manifest.ID, fixture.PluginID)
	}
	if manifest.TokenHub.PluginAPI != "v1" {
		return fmt.Errorf("manifest tokenhub.plugin_api = %q, want v1", manifest.TokenHub.PluginAPI)
	}
	if !contains(manifest.Placement, "background") {
		return errors.New("manifest must declare background placement")
	}
	if manifest.Entry.Backend == nil || manifest.Entry.Backend.Protocol != fixture.Protocol {
		return fmt.Errorf("backend protocol = %q, want %q", manifest.Entry.Backend.Protocol, fixture.Protocol)
	}
	if _, err := safePackageCommand(manifest.Entry.Backend.Command); err != nil {
		return err
	}
	for _, job := range manifest.Capabilities.Background {
		if job.ID == fixture.JobID {
			return validateBackgroundDescriptor(job, fixture)
		}
	}
	return fmt.Errorf("manifest background_jobs missing %q", fixture.JobID)
}

func validateBackgroundDescriptor(job manifestBackgroundJob, fixture backgroundFixture) error {
	if job.Schedule != fixture.Schedule {
		return fmt.Errorf("background job schedule = %q, want %q", job.Schedule, fixture.Schedule)
	}
	if job.Title == "" || job.Capability == "" {
		return fmt.Errorf("background job %q must declare title and capability", job.ID)
	}
	if job.MaxConcurrency <= 0 {
		return fmt.Errorf("background job max_concurrency = %d, want positive", job.MaxConcurrency)
	}
	if job.TimeoutMillis < 0 {
		return fmt.Errorf("background job timeout_millis = %d, want non-negative", job.TimeoutMillis)
	}
	if job.Retry.MaxAttempts < 0 || job.Retry.BackoffMillis < 0 {
		return fmt.Errorf("background job retry policy = %+v, want non-negative values", job.Retry)
	}
	return nil
}

func executeBackgroundCase(ctx context.Context, packageDir string, commandPath string, fixture backgroundFixture, testCase backgroundCase) (backgroundResult, []byte, error) {
	payload, err := json.Marshal(backgroundInvocation{
		PluginID: fixture.PluginID,
		JobID:    fixture.JobID,
		Trigger:  testCase.Trigger,
		Actor:    testCase.Actor,
		Payload:  testCase.Payload,
	})
	if err != nil {
		return backgroundResult{}, nil, fmt.Errorf("encode background invocation: %w", err)
	}
	result, rawOutput, err := executeJSONCommand(ctx, packageDir, commandPath, payload)
	if err != nil {
		return backgroundResult{}, rawOutput, err
	}
	var background backgroundResult
	decoder := json.NewDecoder(bytes.NewReader(result))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&background); err != nil {
		return backgroundResult{}, rawOutput, fmt.Errorf("decode background output: %w", err)
	}
	return background, rawOutput, nil
}

func assertBackgroundResult(testCase backgroundCase, result backgroundResult) error {
	expect := testCase.Expect
	if stringField(result.Data, "resource_id") != expect.ResourceID {
		return fmt.Errorf("resource_id = %q, want %q", stringField(result.Data, "resource_id"), expect.ResourceID)
	}
	if stringField(result.Data, "heartbeat") != expect.Heartbeat {
		return fmt.Errorf("heartbeat = %q, want %q", stringField(result.Data, "heartbeat"), expect.Heartbeat)
	}
	if stringField(result.Data, "trigger") != expect.Trigger {
		return fmt.Errorf("trigger = %q, want %q", stringField(result.Data, "trigger"), expect.Trigger)
	}
	if stringField(result.Data, "actor_id") != expect.ActorID {
		return fmt.Errorf("actor_id = %q, want %q", stringField(result.Data, "actor_id"), expect.ActorID)
	}
	if expect.Count > 0 && int64Field(result.Data, "count") != expect.Count {
		return fmt.Errorf("count = %d, want %d", int64Field(result.Data, "count"), expect.Count)
	}
	if result.Metadata["status"] != expect.MetadataStatus {
		return fmt.Errorf("metadata status = %q, want %q", result.Metadata["status"], expect.MetadataStatus)
	}
	return nil
}
