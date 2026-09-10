package main

import (
	"fmt"
	"gopkg.in/yaml.v3"
	"strings"
)

// Resource types support both shorthand strings and full declarations.
func (resource *ManifestProviderResourceType) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		resource.Type = strings.TrimSpace(value.Value)
		return nil
	case yaml.MappingNode:
		type plain ManifestProviderResourceType
		var parsed plain
		if err := value.Decode(&parsed); err != nil {
			return err
		}
		*resource = ManifestProviderResourceType(parsed)
		return nil
	default:
		return fmt.Errorf("provider resource type must be a string or object")
	}
}
