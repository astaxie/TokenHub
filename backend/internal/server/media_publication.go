package server

import "strings"

// Audio publication and inventory must agree with the native media dispatcher.
// A general media adapter can serve speech but does not imply transcription.
func (s *Server) providerAudioSupport(provider Provider, endpointDeclarations ...string) bool {
	descriptor, found := s.adapterRegistry.Describe(provider.Type)
	_, implemented := resolveTypedAdapter[providerMedia](s.adapterRegistry, provider.Type)
	if !found || !implemented {
		return false
	}
	protocols := adapterDescriptorRouteProtocolSet(descriptor)
	for _, endpoint := range []string{"audio/speech", "audio/transcriptions", "audio/translations"} {
		if protocols[endpoint] && adapterSupports(descriptor, mediaCapabilityForProtocol(endpoint)) && audioEndpointDeclared(endpoint, endpointDeclarations...) {
			return true
		}
	}
	return false
}

// Empty declarations preserve custom and legacy models. Explicit declarations
// must share the same endpoint rather than each matching a different operation.
func audioEndpointDeclared(endpoint string, declarations ...string) bool {
	for _, declaration := range declarations {
		if strings.TrimSpace(declaration) == "" {
			continue
		}
		if !routeProtocolSet(strings.Split(declaration, ","))[endpoint] {
			return false
		}
	}
	return true
}

func isAudioMediaProtocol(protocol string) bool {
	switch protocol {
	case "audio/speech", "audio/transcriptions", "audio/translations":
		return true
	default:
		return false
	}
}

// Read the current inventory so edits cannot leave an incompatible published
// route callable. Missing inventory remains valid for legacy configured routes.
func (s *Server) audioRouteModelEndpoints(providerID, upstreamName string) (string, bool) {
	for _, upstream := range s.store.ListProviderModels() {
		if upstream.ProviderID != providerID || upstream.UpstreamModel != upstreamName {
			continue
		}
		modality := canonicalProviderModality(upstream.Modality)
		// Backfilled inventory inherited the public model's broad category;
		// it is not an explicit upstream operation declaration.
		return upstream.Metadata["endpoints"], upstream.Source == "existing-route" || modality == "" || modality == "audio"
	}
	return "", true
}
