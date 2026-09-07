package plugin

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Masterminds/semver/v3"
)

var (
	ErrPluginDependencyUnsatisfied = errors.New("plugin dependency is unsatisfied")
	ErrPluginDependencyInUse       = errors.New("plugin dependency is required by an enabled plugin")
)

func ValidatePluginVersionConstraint(raw string) error {
	constraint := strings.TrimSpace(raw)
	if constraint == "" {
		return nil
	}
	if _, err := semver.NewConstraint(constraint); err != nil {
		return fmt.Errorf("invalid semantic version constraint %q", raw)
	}
	return nil
}

func ValidateManifestDependencies(manifest Manifest, available []Descriptor) error {
	byID := make(map[string]Descriptor, len(available))
	for _, descriptor := range available {
		byID[strings.TrimSpace(descriptor.ID)] = descriptor
	}
	for _, dependency := range manifest.Dependencies {
		id := strings.TrimSpace(dependency.ID)
		descriptor, ok := byID[id]
		if !ok || !pluginDependencyStatusAvailable(descriptor.Status) {
			return fmt.Errorf("%w: %s is not enabled", ErrPluginDependencyUnsatisfied, id)
		}
		constraintText := strings.TrimSpace(dependency.Version)
		if constraintText == "" {
			continue
		}
		constraint, err := semver.NewConstraint(constraintText)
		if err != nil {
			return fmt.Errorf("%w: %s has invalid constraint %q", ErrPluginDependencyUnsatisfied, id, dependency.Version)
		}
		version, err := semver.NewVersion(strings.TrimSpace(descriptor.Version))
		if err != nil || !constraint.Check(version) {
			return fmt.Errorf("%w: %s version %q does not satisfy %q", ErrPluginDependencyUnsatisfied, id, descriptor.Version, dependency.Version)
		}
	}
	return nil
}

func ValidatePluginDependencySet(available []Descriptor) error {
	for _, descriptor := range available {
		if !pluginDependencyStatusAvailable(descriptor.Status) {
			continue
		}
		manifest := Manifest{ID: descriptor.ID, Dependencies: descriptor.Dependencies}
		if err := ValidateManifestDependencies(manifest, available); err != nil {
			return fmt.Errorf("plugin %s: %w", descriptor.ID, err)
		}
	}
	return nil
}

func ValidatePluginDeactivation(pluginID string, available []Descriptor) error {
	pluginID = strings.TrimSpace(pluginID)
	for _, descriptor := range available {
		if strings.TrimSpace(descriptor.ID) == pluginID || !pluginDependencyStatusAvailable(descriptor.Status) {
			continue
		}
		for _, dependency := range descriptor.Dependencies {
			if strings.TrimSpace(dependency.ID) == pluginID {
				return fmt.Errorf("%w: %s", ErrPluginDependencyInUse, descriptor.ID)
			}
		}
	}
	return nil
}

func pluginDependencyStatusAvailable(status Status) bool {
	switch status {
	case StatusEnabled, StatusMandatory, StatusRollbackAvailable:
		return true
	default:
		return false
	}
}
