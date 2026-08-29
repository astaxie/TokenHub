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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultCommandTimeout = 10 * time.Second
	secretSentinel        = "provider-secret"
)

type manifest struct {
	SchemaVersion int    `yaml:"schema_version"`
	ID            string `yaml:"id"`
	Name          string `yaml:"name"`
	Version       string `yaml:"version"`
	Description   string `yaml:"description"`
	TokenHub      struct {
		PluginAPI string `yaml:"plugin_api"`
	} `yaml:"tokenhub"`
	Kinds     []string `yaml:"kinds"`
	Placement []string `yaml:"placement"`
	Entry     struct {
		Backend *struct {
			Protocol string `yaml:"protocol"`
			Command  string `yaml:"command"`
		} `yaml:"backend"`
	} `yaml:"entry"`
	Capabilities struct {
		ProviderTypes         []string       `yaml:"provider_types"`
		ProviderResourceTypes []string       `yaml:"provider_resource_types"`
		Provider              map[string]any `yaml:"provider"`
		Gateway               []string       `yaml:"gateway"`
	} `yaml:"capabilities"`
	Permissions struct {
		Data struct {
			Read []string `yaml:"read"`
		} `yaml:"data"`
	} `yaml:"permissions"`
	Distribution map[string]any `yaml:"distribution"`
}

type providerFixture struct {
	SchemaVersion               int            `json:"schema_version"`
	Protocol                    string         `json:"protocol"`
	Kind                        string         `json:"kind"`
	ProviderType                string         `json:"provider_type"`
	RequiredGatewayCapabilities []string       `json:"required_gateway_capabilities"`
	Cases                       []providerCase `json:"cases"`
}

type providerCase struct {
	Name          string          `json:"name"`
	Operation     string          `json:"operation"`
	ProviderModel string          `json:"provider_model"`
	Resource      map[string]any  `json:"resource,omitempty"`
	Request       json.RawMessage `json:"request"`
	Expect        providerExpect  `json:"expect"`
}

type providerExpect struct {
	ResponseID      string `json:"response_id,omitempty"`
	ResponseObject  string `json:"response_object,omitempty"`
	Events          int    `json:"events,omitempty"`
	TotalTokens     int64  `json:"total_tokens,omitempty"`
	Status          int    `json:"status,omitempty"`
	CatalogType     string `json:"catalog_type,omitempty"`
	CatalogETag     string `json:"catalog_etag,omitempty"`
	ProbeResourceID string `json:"probe_resource_id,omitempty"`
	ProbeText       string `json:"probe_text,omitempty"`
}

type providerInvocation struct {
	Operation     string          `json:"operation"`
	Provider      map[string]any  `json:"provider"`
	Resource      map[string]any  `json:"resource,omitempty"`
	ProviderModel string          `json:"provider_model"`
	Request       json.RawMessage `json:"request"`
	Credentials   map[string]any  `json:"credentials"`
}

type providerResult struct {
	Response map[string]any   `json:"response,omitempty"`
	Events   []map[string]any `json:"events,omitempty"`
	Usage    map[string]any   `json:"usage,omitempty"`
	Status   int              `json:"status,omitempty"`
	Catalog  map[string]any   `json:"catalog,omitempty"`
	Result   map[string]any   `json:"result,omitempty"`
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "tokenhub-plugin-test:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: tokenhub-plugin-test provider --package <plugin-dir> [--fixture <provider_operations.json>]")
	}
	switch args[0] {
	case "provider":
		return runProvider(ctx, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unsupported contract kind %q", args[0])
	}
}

func runProvider(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("provider", flag.ContinueOnError)
	flags.SetOutput(stderr)
	packageDir := flags.String("package", "", "provider plugin package directory")
	fixturePath := flags.String("fixture", "", "provider operations fixture")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*packageDir) == "" {
		return errors.New("provider --package is required")
	}
	manifest, err := readManifest(*packageDir)
	if err != nil {
		return err
	}
	fixture, err := readProviderFixture(*fixturePath)
	if err != nil {
		return err
	}
	if err := validateProviderManifest(manifest, fixture); err != nil {
		return err
	}
	commandPath, cleanup, err := providerCommandPath(ctx, *packageDir, manifest.Entry.Backend.Command)
	if err != nil {
		return err
	}
	defer cleanup()
	for _, testCase := range fixture.Cases {
		result, rawOutput, err := executeProviderCase(ctx, *packageDir, commandPath, fixture.ProviderType, testCase)
		if err != nil {
			return fmt.Errorf("%s: %w", testCase.Name, err)
		}
		if bytes.Contains(rawOutput, []byte(secretSentinel)) {
			return fmt.Errorf("%s: provider secret leaked to stdout", testCase.Name)
		}
		if err := assertProviderResult(testCase, result); err != nil {
			return fmt.Errorf("%s: %w", testCase.Name, err)
		}
		fmt.Fprintf(stdout, "provider %s: ok\n", testCase.Name)
	}
	fmt.Fprintf(stdout, "provider contract passed (%d cases, manifest %s %s)\n", len(fixture.Cases), manifest.ID, manifest.Version)
	return nil
}

func readManifest(packageDir string) (manifest, error) {
	path := filepath.Join(packageDir, "plugin.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest{}, fmt.Errorf("read plugin manifest: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var value manifest
	if err := decoder.Decode(&value); err != nil {
		return manifest{}, fmt.Errorf("decode plugin manifest: %w", err)
	}
	return value, nil
}

func readProviderFixture(path string) (providerFixture, error) {
	if strings.TrimSpace(path) == "" {
		defaultPath, err := defaultFixturePath()
		if err != nil {
			return providerFixture{}, err
		}
		path = defaultPath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return providerFixture{}, fmt.Errorf("read provider fixture: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var fixture providerFixture
	if err := decoder.Decode(&fixture); err != nil {
		return providerFixture{}, fmt.Errorf("decode provider fixture: %w", err)
	}
	if fixture.SchemaVersion != 1 || fixture.Protocol != "stdio-json-v1" || fixture.Kind != "provider" {
		return providerFixture{}, errors.New("provider fixture must be schema_version 1 stdio-json-v1 provider")
	}
	if len(fixture.Cases) == 0 {
		return providerFixture{}, errors.New("provider fixture must include at least one case")
	}
	return fixture, nil
}

func defaultFixturePath() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("cannot resolve default fixture path")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	return filepath.Join(root, "contract-tests", "protocol", "stdio-json-v1", "provider_operations.json"), nil
}

func validateProviderManifest(manifest manifest, fixture providerFixture) error {
	if manifest.SchemaVersion != 1 {
		return fmt.Errorf("manifest schema_version = %d, want 1", manifest.SchemaVersion)
	}
	if manifest.TokenHub.PluginAPI != "v1" {
		return fmt.Errorf("manifest tokenhub.plugin_api = %q, want v1", manifest.TokenHub.PluginAPI)
	}
	if !contains(manifest.Kinds, "provider") {
		return errors.New("manifest must declare provider kind")
	}
	if !contains(manifest.Placement, "gateway_chain") {
		return errors.New("manifest must declare gateway_chain placement")
	}
	if manifest.Entry.Backend == nil {
		return errors.New("manifest must declare entry.backend")
	}
	if manifest.Entry.Backend.Protocol != fixture.Protocol {
		return fmt.Errorf("backend protocol = %q, want %q", manifest.Entry.Backend.Protocol, fixture.Protocol)
	}
	if _, err := safePackageCommand(manifest.Entry.Backend.Command); err != nil {
		return err
	}
	if !contains(manifest.Capabilities.ProviderTypes, fixture.ProviderType) {
		return fmt.Errorf("manifest provider_types missing %q", fixture.ProviderType)
	}
	for _, capability := range fixture.RequiredGatewayCapabilities {
		if !contains(manifest.Capabilities.Gateway, capability) {
			return fmt.Errorf("manifest gateway capabilities missing %q", capability)
		}
	}
	if !contains(manifest.Permissions.Data.Read, "provider_credentials") {
		return errors.New("provider sample must declare provider_credentials read permission")
	}
	return nil
}

func providerCommandPath(ctx context.Context, packageDir string, command string) (string, func(), error) {
	relativeCommand, err := safePackageCommand(command)
	if err != nil {
		return "", func() {}, err
	}
	commandPath := filepath.Join(packageDir, relativeCommand)
	if info, err := os.Stat(commandPath); err == nil && !info.IsDir() {
		return commandPath, func() {}, nil
	}
	tempDir, err := os.MkdirTemp("", "tokenhub-plugin-test-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("create build directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tempDir) }
	binaryPath := filepath.Join(tempDir, filepath.Base(relativeCommand))
	if runtime.GOOS == "windows" {
		binaryPath += ".exe"
	}
	buildCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", binaryPath, ".")
	build.Dir = packageDir
	var stderr bytes.Buffer
	build.Stderr = &stderr
	if err := build.Run(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("build provider sample: %s", strings.TrimSpace(stderr.String()))
	}
	return binaryPath, cleanup, nil
}

func safePackageCommand(command string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", errors.New("backend command is required")
	}
	if filepath.IsAbs(command) {
		return "", errors.New("backend command must be package-relative")
	}
	clean := filepath.Clean(command)
	if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", errors.New("backend command must not escape the package directory")
	}
	return clean, nil
}

func executeProviderCase(ctx context.Context, packageDir string, commandPath string, providerType string, testCase providerCase) (providerResult, []byte, error) {
	payload, err := json.Marshal(providerInvocation{
		Operation:     testCase.Operation,
		Provider:      map[string]any{"id": "prv_external_mock", "type": providerType},
		Resource:      testCase.Resource,
		ProviderModel: testCase.ProviderModel,
		Request:       testCase.Request,
		Credentials:   map[string]any{"api_key": secretSentinel},
	})
	if err != nil {
		return providerResult{}, nil, fmt.Errorf("encode invocation: %w", err)
	}
	runCtx, cancel := context.WithTimeout(ctx, defaultCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, commandPath)
	cmd.Dir = packageDir
	cmd.Stdin = bytes.NewReader(payload)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if runCtx.Err() != nil {
			return providerResult{}, stdout.Bytes(), runCtx.Err()
		}
		return providerResult{}, stdout.Bytes(), fmt.Errorf("command failed: %s", strings.TrimSpace(stderr.String()))
	}
	var result providerResult
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return providerResult{}, stdout.Bytes(), fmt.Errorf("decode provider output: %w", err)
	}
	return result, stdout.Bytes(), nil
}

func assertProviderResult(testCase providerCase, result providerResult) error {
	expect := testCase.Expect
	if expect.ResponseID != "" && stringField(result.Response, "id") != expect.ResponseID {
		return fmt.Errorf("response id = %q, want %q", stringField(result.Response, "id"), expect.ResponseID)
	}
	if expect.ResponseObject != "" && stringField(result.Response, "object") != expect.ResponseObject {
		return fmt.Errorf("response object = %q, want %q", stringField(result.Response, "object"), expect.ResponseObject)
	}
	if expect.Events > 0 && len(result.Events) != expect.Events {
		return fmt.Errorf("events = %d, want %d", len(result.Events), expect.Events)
	}
	if expect.TotalTokens > 0 && int64Field(result.Usage, "total_tokens") != expect.TotalTokens {
		return fmt.Errorf("total_tokens = %d, want %d", int64Field(result.Usage, "total_tokens"), expect.TotalTokens)
	}
	if expect.Status > 0 && result.Status != expect.Status {
		return fmt.Errorf("status = %d, want %d", result.Status, expect.Status)
	}
	if expect.CatalogType != "" && stringField(result.Catalog, "type") != expect.CatalogType {
		return fmt.Errorf("catalog type = %q, want %q", stringField(result.Catalog, "type"), expect.CatalogType)
	}
	if expect.CatalogETag != "" && stringField(result.Catalog, "etag") != expect.CatalogETag {
		return fmt.Errorf("catalog etag = %q, want %q", stringField(result.Catalog, "etag"), expect.CatalogETag)
	}
	if expect.ProbeResourceID != "" && stringField(result.Result, "resource_id") != expect.ProbeResourceID {
		return fmt.Errorf("probe resource_id = %q, want %q", stringField(result.Result, "resource_id"), expect.ProbeResourceID)
	}
	if expect.ProbeText != "" && stringField(result.Result, "output_text") != expect.ProbeText {
		return fmt.Errorf("probe output_text = %q, want %q", stringField(result.Result, "output_text"), expect.ProbeText)
	}
	return nil
}

func stringField(value map[string]any, key string) string {
	if value == nil {
		return ""
	}
	text, _ := value[key].(string)
	return text
}

func int64Field(value map[string]any, key string) int64 {
	if value == nil {
		return 0
	}
	switch number := value[key].(type) {
	case float64:
		return int64(number)
	case int64:
		return number
	default:
		return 0
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
