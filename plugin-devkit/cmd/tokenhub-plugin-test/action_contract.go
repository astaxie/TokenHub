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

type actionFixture struct {
	SchemaVersion int          `json:"schema_version"`
	Protocol      string       `json:"protocol"`
	Kind          string       `json:"kind"`
	PluginID      string       `json:"plugin_id"`
	ActionID      string       `json:"action_id"`
	Cases         []actionCase `json:"cases"`
}

type actionCase struct {
	Name    string         `json:"name"`
	Actor   map[string]any `json:"actor"`
	Payload map[string]any `json:"payload"`
	Expect  actionExpect   `json:"expect"`
}

type actionExpect struct {
	ResourceID     string `json:"resource_id"`
	Message        string `json:"message"`
	DryRun         bool   `json:"dry_run"`
	ActorID        string `json:"actor_id"`
	MetadataStatus string `json:"metadata_status"`
}

type actionInvocation struct {
	PluginID string         `json:"plugin_id"`
	ActionID string         `json:"action_id"`
	Actor    map[string]any `json:"actor"`
	Payload  map[string]any `json:"payload,omitempty"`
}

type actionResult struct {
	Data        map[string]any    `json:"data,omitempty"`
	RedirectURL string            `json:"redirect_url,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

func runAction(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("action", flag.ContinueOnError)
	flags.SetOutput(stderr)
	packageDir := flags.String("package", "", "management action plugin package directory")
	fixturePath := flags.String("fixture", "", "action invocation fixture")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*packageDir) == "" {
		return errors.New("action --package is required")
	}
	manifest, err := readManifest(*packageDir)
	if err != nil {
		return err
	}
	fixture, err := readActionFixture(*fixturePath)
	if err != nil {
		return err
	}
	if err := validateActionManifest(manifest, fixture); err != nil {
		return err
	}
	commandPath, cleanup, err := pluginCommandPath(ctx, *packageDir, manifest.Entry.Backend.Command, "action")
	if err != nil {
		return err
	}
	defer cleanup()
	for _, testCase := range fixture.Cases {
		result, rawOutput, err := executeActionCase(ctx, *packageDir, commandPath, fixture, testCase)
		if err != nil {
			return fmt.Errorf("%s: %w", testCase.Name, err)
		}
		if bytes.Contains(rawOutput, []byte(secretSentinel)) {
			return fmt.Errorf("%s: provider secret leaked to stdout", testCase.Name)
		}
		if err := assertActionResult(testCase, result); err != nil {
			return fmt.Errorf("%s: %w", testCase.Name, err)
		}
		fmt.Fprintf(stdout, "action %s: ok\n", testCase.Name)
	}
	fmt.Fprintf(stdout, "action contract passed (%d cases, manifest %s %s)\n", len(fixture.Cases), manifest.ID, manifest.Version)
	return nil
}

func readActionFixture(path string) (actionFixture, error) {
	if strings.TrimSpace(path) == "" {
		defaultPath, err := defaultActionFixturePath()
		if err != nil {
			return actionFixture{}, err
		}
		path = defaultPath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return actionFixture{}, fmt.Errorf("read action fixture: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var fixture actionFixture
	if err := decoder.Decode(&fixture); err != nil {
		return actionFixture{}, fmt.Errorf("decode action fixture: %w", err)
	}
	if fixture.SchemaVersion != 1 || fixture.Protocol != "stdio-json-v1" || fixture.Kind != "management_action" {
		return actionFixture{}, errors.New("action fixture must be schema_version 1 stdio-json-v1 management_action")
	}
	if fixture.PluginID == "" || fixture.ActionID == "" || len(fixture.Cases) == 0 {
		return actionFixture{}, errors.New("action fixture must include plugin_id, action_id, and at least one case")
	}
	return fixture, nil
}

func defaultActionFixturePath() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("cannot resolve default action fixture path")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	return filepath.Join(root, "contract-tests", "protocol", "stdio-json-v1", "action_invocations.json"), nil
}

func validateActionManifest(manifest manifest, fixture actionFixture) error {
	if manifest.SchemaVersion != 1 {
		return fmt.Errorf("manifest schema_version = %d, want 1", manifest.SchemaVersion)
	}
	if manifest.ID != fixture.PluginID {
		return fmt.Errorf("manifest id = %q, want %q", manifest.ID, fixture.PluginID)
	}
	if manifest.TokenHub.PluginAPI != "v1" {
		return fmt.Errorf("manifest tokenhub.plugin_api = %q, want v1", manifest.TokenHub.PluginAPI)
	}
	if !contains(manifest.Placement, "management_action") {
		return errors.New("manifest must declare management_action placement")
	}
	if manifest.Entry.Backend == nil || manifest.Entry.Backend.Protocol != fixture.Protocol {
		return fmt.Errorf("backend protocol = %q, want %q", manifest.Entry.Backend.Protocol, fixture.Protocol)
	}
	if _, err := safePackageCommand(manifest.Entry.Backend.Command); err != nil {
		return err
	}
	for _, action := range manifest.Capabilities.Actions {
		if action.ID == fixture.ActionID {
			if action.Kind == "" || action.Title == "" {
				return fmt.Errorf("action %q must declare kind and title", action.ID)
			}
			return nil
		}
	}
	return fmt.Errorf("manifest actions missing %q", fixture.ActionID)
}

func executeActionCase(ctx context.Context, packageDir string, commandPath string, fixture actionFixture, testCase actionCase) (actionResult, []byte, error) {
	payload, err := json.Marshal(actionInvocation{
		PluginID: fixture.PluginID,
		ActionID: fixture.ActionID,
		Actor:    testCase.Actor,
		Payload:  testCase.Payload,
	})
	if err != nil {
		return actionResult{}, nil, fmt.Errorf("encode invocation: %w", err)
	}
	result, rawOutput, err := executeJSONCommand(ctx, packageDir, commandPath, payload)
	if err != nil {
		return actionResult{}, rawOutput, err
	}
	var action actionResult
	decoder := json.NewDecoder(bytes.NewReader(result))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&action); err != nil {
		return actionResult{}, rawOutput, fmt.Errorf("decode action output: %w", err)
	}
	return action, rawOutput, nil
}

func assertActionResult(testCase actionCase, result actionResult) error {
	expect := testCase.Expect
	if stringField(result.Data, "resource_id") != expect.ResourceID {
		return fmt.Errorf("resource_id = %q, want %q", stringField(result.Data, "resource_id"), expect.ResourceID)
	}
	if stringField(result.Data, "message") != expect.Message {
		return fmt.Errorf("message = %q, want %q", stringField(result.Data, "message"), expect.Message)
	}
	if boolField(result.Data, "dry_run") != expect.DryRun {
		return fmt.Errorf("dry_run = %v, want %v", boolField(result.Data, "dry_run"), expect.DryRun)
	}
	if stringField(result.Data, "actor_id") != expect.ActorID {
		return fmt.Errorf("actor_id = %q, want %q", stringField(result.Data, "actor_id"), expect.ActorID)
	}
	if result.Metadata["status"] != expect.MetadataStatus {
		return fmt.Errorf("metadata status = %q, want %q", result.Metadata["status"], expect.MetadataStatus)
	}
	if result.RedirectURL != "" {
		return fmt.Errorf("redirect_url = %q, want empty for local echo action", result.RedirectURL)
	}
	return nil
}

func boolField(value map[string]any, key string) bool {
	if value == nil {
		return false
	}
	flag, _ := value[key].(bool)
	return flag
}
