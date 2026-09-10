package plugin

import (
	"strings"
	"testing"
)

func TestParseManifestAcceptsDeclarativeProviderPluginAPIV2(t *testing.T) {
	manifest, err := ParseManifest([]byte(`
schema_version: 2
id: example.provider
name: Example Provider
version: 1.0.0
summary: Connect TokenHub to Example.
category: provider_integration
host_adapter: openai_compatible
tokenhub:
  plugin_api: v2
  min_core: 0.7.0
kinds: [provider]
placement: []
capabilities:
  provider_types: [openai_compatible]
permissions:
  network:
    allow: []
  data:
    read: []
    write: []
`))
	if err != nil {
		t.Fatalf("parse plugin API v2 manifest: %v", err)
	}
	descriptor := manifest.Descriptor()
	if descriptor.Summary != "Connect TokenHub to Example." || descriptor.Category != CategoryProviderIntegration || descriptor.HostAdapter != "openai_compatible" {
		t.Fatalf("descriptor v2 metadata = %+v", descriptor)
	}
}

func TestParseManifestRejectsMismatchedSchemaAndPluginAPI(t *testing.T) {
	_, err := ParseManifest([]byte(`
schema_version: 2
id: example.legacy
name: Example Legacy
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds: [extension]
`))
	if err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("error = %v, want schema version mismatch", err)
	}
}

func TestParseManifestRejectsPriorityInPluginAPIV2(t *testing.T) {
	_, err := ParseManifest([]byte(`
schema_version: 2
id: example.pipeline
name: Example Pipeline
version: 1.0.0
summary: Transform requests.
category: request_pipeline
tokenhub:
  plugin_api: v2
kinds: [extension]
placement: [gateway_chain]
capabilities:
  hooks:
    - id: transform
      stage: request_transform
      priority: 100
permissions:
  data:
    read: []
    write: []
`))
	if err == nil || !strings.Contains(err.Error(), "use before/after") {
		t.Fatalf("error = %v, want plugin API v2 priority rejection", err)
	}
}

func TestParseManifestRejectsInvalidDependencyConstraint(t *testing.T) {
	_, err := ParseManifest([]byte(`
schema_version: 2
id: example.automation
name: Example Automation
version: 1.0.0
summary: Runs example automation.
category: automation
tokenhub:
  plugin_api: v2
kinds: [extension]
placement: []
dependencies:
  - id: example.core
    version: not-a-constraint
permissions:
  data:
    read: []
    write: []
`))
	if err == nil || !strings.Contains(err.Error(), "invalid semantic version constraint") {
		t.Fatalf("error = %v, want invalid dependency constraint", err)
	}
}
