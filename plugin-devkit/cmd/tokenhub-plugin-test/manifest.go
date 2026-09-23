package main

import (
	"fmt"
	"strings"
)

type manifest = Manifest
type manifestHook = GatewayHookManifest
type manifestBackgroundJob = BackgroundJobManifest

func validateManifestAPI(value manifest) error {
	switch {
	case value.SchemaVersion == 1 && value.TokenHub.PluginAPI == "v1":
		return nil
	case value.SchemaVersion == 2 && value.TokenHub.PluginAPI == "v2":
		if strings.TrimSpace(value.Summary) == "" {
			return fmt.Errorf("plugin API v2 manifest must declare summary")
		}
		switch value.Category {
		case "provider_integration", "request_pipeline", "ui_template", "automation":
			return nil
		default:
			return fmt.Errorf("plugin API v2 manifest category %q is unsupported", value.Category)
		}
	default:
		return fmt.Errorf("unsupported manifest schema/plugin API pair %d/%q", value.SchemaVersion, value.TokenHub.PluginAPI)
	}
}
