package plugin

import (
	"errors"
	"testing"
)

func TestValidatePluginVersionConstraint(t *testing.T) {
	t.Parallel()
	for _, constraint := range []string{"", ">= 1.2.0, < 2.0.0", "^1.4.0", "~2.1.3"} {
		if err := ValidatePluginVersionConstraint(constraint); err != nil {
			t.Fatalf("constraint %q: %v", constraint, err)
		}
	}
	if err := ValidatePluginVersionConstraint("not a version"); err == nil {
		t.Fatal("invalid version constraint was accepted")
	}
}

func TestValidateManifestDependenciesRequiresEnabledCompatiblePlugin(t *testing.T) {
	t.Parallel()
	manifest := Manifest{Dependencies: []ManifestDependency{{ID: "tokenhub.core", Version: ">= 1.2.0, < 2.0.0"}}}
	compatible := Descriptor{ID: "tokenhub.core", Version: "1.4.0", Status: StatusEnabled}
	if err := ValidateManifestDependencies(manifest, []Descriptor{compatible}); err != nil {
		t.Fatalf("compatible dependency: %v", err)
	}

	for _, tc := range []struct {
		name      string
		available []Descriptor
	}{
		{name: "missing"},
		{name: "disabled", available: []Descriptor{{ID: "tokenhub.core", Version: "1.4.0", Status: StatusDisabled}}},
		{name: "incompatible", available: []Descriptor{{ID: "tokenhub.core", Version: "2.0.0", Status: StatusEnabled}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateManifestDependencies(manifest, tc.available); !errors.Is(err, ErrPluginDependencyUnsatisfied) {
				t.Fatalf("error = %v, want ErrPluginDependencyUnsatisfied", err)
			}
		})
	}
}

func TestValidatePluginDependencySetRejectsIncompatibleReplacement(t *testing.T) {
	t.Parallel()
	descriptors := []Descriptor{
		{ID: "tokenhub.core", Version: "2.0.0", Status: StatusEnabled},
		{ID: "tokenhub.consumer", Version: "1.0.0", Status: StatusEnabled, Dependencies: []ManifestDependency{{ID: "tokenhub.core", Version: "^1.0.0"}}},
	}
	if err := ValidatePluginDependencySet(descriptors); !errors.Is(err, ErrPluginDependencyUnsatisfied) {
		t.Fatalf("error = %v, want ErrPluginDependencyUnsatisfied", err)
	}
}

func TestValidatePluginDeactivationRejectsEnabledDependent(t *testing.T) {
	t.Parallel()
	descriptors := []Descriptor{
		{ID: "tokenhub.core", Version: "1.0.0", Status: StatusEnabled},
		{ID: "tokenhub.consumer", Version: "1.0.0", Status: StatusEnabled, Dependencies: []ManifestDependency{{ID: "tokenhub.core"}}},
	}
	if err := ValidatePluginDeactivation("tokenhub.core", descriptors); !errors.Is(err, ErrPluginDependencyInUse) {
		t.Fatalf("error = %v, want ErrPluginDependencyInUse", err)
	}
	descriptors[1].Status = StatusDisabled
	if err := ValidatePluginDeactivation("tokenhub.core", descriptors); err != nil {
		t.Fatalf("disabled dependent blocked deactivation: %v", err)
	}
}
