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

type hookFixture struct {
	SchemaVersion int        `json:"schema_version"`
	Protocol      string     `json:"protocol"`
	Kind          string     `json:"kind"`
	PluginID      string     `json:"plugin_id"`
	HookID        string     `json:"hook_id"`
	Stage         string     `json:"stage"`
	FailurePolicy string     `json:"failure_policy"`
	Reads         []string   `json:"reads"`
	Writes        []string   `json:"writes"`
	Cases         []hookCase `json:"cases"`
}

type hookCase struct {
	Name               string     `json:"name"`
	Input              hookInput  `json:"input"`
	ForbiddenFragments []string   `json:"forbidden_fragments"`
	Expect             hookExpect `json:"expect"`
}

type hookInput struct {
	RequestID string                     `json:"request_id"`
	Stage     string                     `json:"stage"`
	Envelope  map[string]any             `json:"envelope"`
	Data      map[string]json.RawMessage `json:"data"`
}

type hookExpect struct {
	Decision   string `json:"decision"`
	AuditEvent string `json:"audit_event"`
	SawAudit   bool   `json:"saw_audit"`
	SawUsage   bool   `json:"saw_usage"`
	Writes     int    `json:"writes"`
}

type hookResult struct {
	Decision    string                     `json:"decision"`
	Writes      map[string]json.RawMessage `json:"writes,omitempty"`
	AuditEvents []json.RawMessage          `json:"audit_events,omitempty"`
}

func runHook(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("hook", flag.ContinueOnError)
	flags.SetOutput(stderr)
	packageDir := flags.String("package", "", "gateway hook plugin package directory")
	fixturePath := flags.String("fixture", "", "gateway hook fixture")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*packageDir) == "" {
		return errors.New("hook --package is required")
	}
	manifest, err := readManifest(*packageDir)
	if err != nil {
		return err
	}
	fixture, err := readHookFixture(*fixturePath)
	if err != nil {
		return err
	}
	if err := validateHookManifest(manifest, fixture); err != nil {
		return err
	}
	commandPath, cleanup, err := pluginCommandPath(ctx, *packageDir, manifest.Entry.Backend.Command, "hook")
	if err != nil {
		return err
	}
	defer cleanup()
	for _, testCase := range fixture.Cases {
		result, rawOutput, err := executeHookCase(ctx, *packageDir, commandPath, testCase)
		if err != nil {
			return fmt.Errorf("%s: %w", testCase.Name, err)
		}
		for _, fragment := range testCase.ForbiddenFragments {
			if bytes.Contains(rawOutput, []byte(fragment)) {
				return fmt.Errorf("%s: forbidden fragment %q leaked to stdout", testCase.Name, fragment)
			}
		}
		if err := assertHookResult(testCase, result); err != nil {
			return fmt.Errorf("%s: %w", testCase.Name, err)
		}
		fmt.Fprintf(stdout, "hook %s: ok\n", testCase.Name)
	}
	fmt.Fprintf(stdout, "hook contract passed (%d cases, manifest %s %s)\n", len(fixture.Cases), manifest.ID, manifest.Version)
	return nil
}

func readHookFixture(path string) (hookFixture, error) {
	if strings.TrimSpace(path) == "" {
		defaultPath, err := defaultHookFixturePath()
		if err != nil {
			return hookFixture{}, err
		}
		path = defaultPath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return hookFixture{}, fmt.Errorf("read hook fixture: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var fixture hookFixture
	if err := decoder.Decode(&fixture); err != nil {
		return hookFixture{}, fmt.Errorf("decode hook fixture: %w", err)
	}
	if fixture.SchemaVersion != 1 || fixture.Protocol != "stdio-json-v1" || fixture.Kind != "gateway_hook" {
		return hookFixture{}, errors.New("hook fixture must be schema_version 1 stdio-json-v1 gateway_hook")
	}
	if fixture.PluginID == "" || fixture.HookID == "" || fixture.Stage == "" || len(fixture.Cases) == 0 {
		return hookFixture{}, errors.New("hook fixture must include plugin_id, hook_id, stage, and at least one case")
	}
	return fixture, nil
}

func defaultHookFixturePath() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("cannot resolve default hook fixture path")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	return filepath.Join(root, "contract-tests", "protocol", "stdio-json-v1", "gateway_hook_trace.json"), nil
}

func validateHookManifest(manifest manifest, fixture hookFixture) error {
	if manifest.SchemaVersion != 1 {
		return fmt.Errorf("manifest schema_version = %d, want 1", manifest.SchemaVersion)
	}
	if manifest.ID != fixture.PluginID {
		return fmt.Errorf("manifest id = %q, want %q", manifest.ID, fixture.PluginID)
	}
	if manifest.TokenHub.PluginAPI != "v1" {
		return fmt.Errorf("manifest tokenhub.plugin_api = %q, want v1", manifest.TokenHub.PluginAPI)
	}
	if !contains(manifest.Kinds, "extension") || !contains(manifest.Placement, "gateway_chain") {
		return errors.New("manifest must declare extension kind and gateway_chain placement")
	}
	if manifest.Entry.Backend == nil || manifest.Entry.Backend.Protocol != fixture.Protocol {
		return fmt.Errorf("backend protocol = %q, want %q", manifest.Entry.Backend.Protocol, fixture.Protocol)
	}
	if _, err := safePackageCommand(manifest.Entry.Backend.Command); err != nil {
		return err
	}
	for _, hook := range manifest.Capabilities.Hooks {
		if hook.ID == fixture.HookID {
			return validateHookDescriptor(hook, fixture)
		}
	}
	return fmt.Errorf("manifest hooks missing %q", fixture.HookID)
}

func validateHookDescriptor(hook manifestHook, fixture hookFixture) error {
	if hook.Stage != fixture.Stage {
		return fmt.Errorf("hook stage = %q, want %q", hook.Stage, fixture.Stage)
	}
	if hook.FailurePolicy != fixture.FailurePolicy {
		return fmt.Errorf("hook failure_policy = %q, want %q", hook.FailurePolicy, fixture.FailurePolicy)
	}
	if !sameStrings(hook.Reads, fixture.Reads) || !sameStrings(hook.Writes, fixture.Writes) {
		return fmt.Errorf("hook reads/writes = %v/%v, want %v/%v", hook.Reads, hook.Writes, fixture.Reads, fixture.Writes)
	}
	return nil
}

func executeHookCase(ctx context.Context, packageDir string, commandPath string, testCase hookCase) (hookResult, []byte, error) {
	payload, err := json.Marshal(testCase.Input)
	if err != nil {
		return hookResult{}, nil, fmt.Errorf("encode hook input: %w", err)
	}
	result, rawOutput, err := executeJSONCommand(ctx, packageDir, commandPath, payload)
	if err != nil {
		return hookResult{}, rawOutput, err
	}
	var hook hookResult
	decoder := json.NewDecoder(bytes.NewReader(result))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&hook); err != nil {
		return hookResult{}, rawOutput, fmt.Errorf("decode hook output: %w", err)
	}
	return hook, rawOutput, nil
}

func assertHookResult(testCase hookCase, result hookResult) error {
	expect := testCase.Expect
	if result.Decision != expect.Decision {
		return fmt.Errorf("decision = %q, want %q", result.Decision, expect.Decision)
	}
	if len(result.Writes) != expect.Writes {
		return fmt.Errorf("writes = %d, want %d", len(result.Writes), expect.Writes)
	}
	if !auditEventContains(result.AuditEvents, expect.AuditEvent) {
		return fmt.Errorf("audit events = %s, want event %q", result.AuditEvents, expect.AuditEvent)
	}
	if auditEventFlag(result.AuditEvents, "saw_audit") != expect.SawAudit {
		return fmt.Errorf("saw_audit flag mismatch in audit events %s", result.AuditEvents)
	}
	if auditEventFlag(result.AuditEvents, "saw_usage") != expect.SawUsage {
		return fmt.Errorf("saw_usage flag mismatch in audit events %s", result.AuditEvents)
	}
	return nil
}

func auditEventContains(events []json.RawMessage, event string) bool {
	for _, raw := range events {
		var value map[string]any
		if json.Unmarshal(raw, &value) == nil && value["event"] == event {
			return true
		}
	}
	return false
}

func auditEventFlag(events []json.RawMessage, key string) bool {
	for _, raw := range events {
		var value map[string]any
		if json.Unmarshal(raw, &value) == nil {
			flag, _ := value[key].(bool)
			if flag {
				return true
			}
		}
	}
	return false
}

func sameStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
