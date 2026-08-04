package litellm

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrVersionUnknown is returned when the LiteLLM version marker cannot be
// detected in the config.
var ErrVersionUnknown = errors.New("litellm: version marker not found in config")

// ErrVersionUnsupported is returned when the LiteLLM version is outside the
// supported range.
var ErrVersionUnsupported = errors.New("litellm: version outside supported range")

type semver struct {
	Major int
	Minor int
	Patch int
}

func ValidateSupportedVersion(version string) error {
	v, err := parseVersion(normalizeVersion(version))
	if err != nil {
		return fmt.Errorf("%w: parse version %q: %w", ErrVersionUnknown, version, err)
	}
	min, _ := parseVersion("1.52.0")
	max, _ := parseVersion("1.70.0")
	if compareSemver(v, min) < 0 || compareSemver(v, max) >= 0 {
		return fmt.Errorf("%w: version %q, supported range is %s", ErrVersionUnsupported, version, SupportedVersionRange)
	}
	return nil
}

func parseVersion(version string) (semver, error) {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return semver{}, fmt.Errorf("invalid semver %q", version)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return semver{}, err
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return semver{}, err
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return semver{}, err
	}
	return semver{Major: major, Minor: minor, Patch: patch}, nil
}

func normalizeVersion(version string) string {
	version = strings.TrimSpace(version)
	version = strings.TrimPrefix(version, "v")
	return version
}

func compareSemver(a, b semver) int {
	if a.Major != b.Major {
		if a.Major < b.Major {
			return -1
		}
		return 1
	}
	if a.Minor != b.Minor {
		if a.Minor < b.Minor {
			return -1
		}
		return 1
	}
	if a.Patch != b.Patch {
		if a.Patch < b.Patch {
			return -1
		}
		return 1
	}
	return 0
}
