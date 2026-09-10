package plugin

import (
	"fmt"
	"strings"

	"github.com/Masterminds/semver/v3"
)

func validateMarketplaceCoreRange(minimum, maximum string) error {
	var min, max pluginCoreVersion
	var err error
	if strings.TrimSpace(minimum) != "" {
		if min, err = parsePluginCoreVersion(minimum); err != nil {
			return fmt.Errorf("tokenhub.min_core is invalid: %w", err)
		}
	}
	if strings.TrimSpace(maximum) != "" {
		if max, err = parsePluginCoreVersion(maximum); err != nil {
			return fmt.Errorf("invalid max_core: %w", err)
		}
		if strings.TrimSpace(minimum) != "" && comparePluginCoreVersion(min, max) > 0 {
			return fmt.Errorf("min_core cannot exceed max_core")
		}
	}
	return nil
}

func marketplaceRequiredFeaturesSupported(compat MarketplaceReleaseCompatibility) bool {
	supported := map[string]bool{}
	for _, api := range SupportedPluginAPICompatibility() {
		for _, feature := range api.FeatureFlags {
			supported[feature] = true
		}
	}
	for _, feature := range compat.RequiredFeatures {
		if !supported[strings.TrimSpace(feature)] {
			return false
		}
	}
	return true
}

// MarketplaceVersionGreater treats unparseable local versions conservatively.
func MarketplaceVersionGreater(available, installed string) bool {
	left, leftErr := semver.StrictNewVersion(strings.TrimSpace(available))
	right, rightErr := semver.StrictNewVersion(strings.TrimSpace(installed))
	return leftErr == nil && rightErr == nil && left.GreaterThan(right)
}
