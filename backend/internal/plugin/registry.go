package plugin

import (
	"fmt"
	"sort"
	"strings"
)

type Registry struct {
	plugins map[string]Descriptor
}

func NewRegistry() *Registry {
	return &Registry{plugins: map[string]Descriptor{}}
}

func (r *Registry) Register(descriptor Descriptor) error {
	if r == nil {
		return fmt.Errorf("plugin registry is not configured")
	}
	hasStatus := strings.TrimSpace(string(descriptor.Status)) != ""
	descriptor = NormalizeDescriptor(descriptor)
	descriptor.ID = strings.TrimSpace(descriptor.ID)
	if descriptor.ID == "" {
		return fmt.Errorf("plugin id is required")
	}
	if existing, ok := r.plugins[descriptor.ID]; ok {
		if !hasStatus {
			descriptor.Status = ""
		}
		descriptor = mergeDescriptors(existing, descriptor)
	}
	if descriptor.Source == "" {
		descriptor.Source = SourceLocalFile
	}
	r.plugins[descriptor.ID] = descriptor
	return nil
}

func (r *Registry) Describe(id string) (Descriptor, bool) {
	if r == nil {
		return Descriptor{}, false
	}
	descriptor, ok := r.plugins[id]
	return descriptor, ok
}

func (r *Registry) List() []Descriptor {
	if r == nil {
		return nil
	}
	items := make([]Descriptor, 0, len(r.plugins))
	for _, descriptor := range r.plugins {
		items = append(items, descriptor)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

func mergeDescriptors(existing Descriptor, next Descriptor) Descriptor {
	if next.Name == "" {
		next.Name = existing.Name
	}
	if next.Version == "" {
		next.Version = existing.Version
	}
	if next.Source == "" {
		next.Source = existing.Source
	}
	if next.Status == "" {
		next.Status = existing.Status
	}
	if next.Distribution == nil {
		next.Distribution = existing.Distribution
	}
	next.Kinds = append(existing.Kinds, next.Kinds...)
	next.Placements = append(existing.Placements, next.Placements...)
	next.Capabilities = append(existing.Capabilities, next.Capabilities...)
	return NormalizeDescriptor(next)
}
