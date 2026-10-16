package server

import "strings"

// Audio publication and inventory must agree with the native media dispatcher.
// A general media adapter can serve speech but does not imply transcription.
func (s *Server) providerAudioSupport(provider Provider, endpoints string) bool {
	descriptor, found := s.adapterRegistry.Describe(provider.Type)
	_, implemented := resolveTypedAdapter[providerMedia](s.adapterRegistry, provider.Type)
	if !found || !implemented {
		return false
	}
	if strings.TrimSpace(endpoints) == "" {
		return adapterSupports(descriptor, AdapterCapabilityMedia) || adapterSupports(descriptor, AdapterCapabilityTranscription)
	}
	protocols := adapterDescriptorRouteProtocolSet(descriptor)
	for _, endpoint := range strings.Split(endpoints, ",") {
		endpoint = strings.ToLower(strings.TrimSpace(endpoint))
		switch endpoint {
		case "audio/speech", "audio/transcriptions", "audio/translations":
			if protocols[endpoint] && adapterSupports(descriptor, mediaCapabilityForProtocol(endpoint)) {
				return true
			}
		}
	}
	return false
}
