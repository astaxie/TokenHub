package server

import (
	"net/http"
	"strings"
)

const imageRequestSizePolicyGPTImage2 = "gpt-image-2"
const currentImageOutputLimit = 1

func defaultImageRequestQualities() []string {
	return []string{"auto", "low", "medium", "high"}
}

func defaultImageResponseFormats() []string {
	return []string{"url", "b64_json"}
}

func (s *Server) applyImageGenerationRequestAliases(r *http.Request, request *imageGenerationRequest) {
	if s == nil || request == nil {
		return
	}
	model := strings.TrimSpace(request.Model)
	for _, profile := range s.providerImageCapabilityRouteProfiles() {
		profile.withDefaults()
		if profile.RequestAliasModel == "" || profile.PublicModel == "" || model != profile.RequestAliasModel {
			continue
		}
		if !imageGenerationRequestAliasMatches(r, profile) {
			continue
		}
		request.Model = profile.PublicModel
		if profile.RequestAliasResponseFormat != "" {
			request.ResponseFormat = profile.RequestAliasResponseFormat
		}
		return
	}
}

func imageGenerationRequestAliasMatches(r *http.Request, profile providerImageCapabilityRouteProfile) bool {
	if r == nil {
		return false
	}
	if profile.RequestAliasHeader != "" && strings.TrimSpace(r.Header.Get(profile.RequestAliasHeader)) != "" {
		return true
	}
	originator := strings.ToLower(strings.TrimSpace(r.Header.Get("originator")))
	return profile.RequestAliasOriginator != "" && strings.HasPrefix(originator, profile.RequestAliasOriginator)
}

func (s *Server) imageGenerationModelsAndDefault() ([]string, string) {
	if s == nil || s.pluginActions == nil {
		return []string{codexImageModelName, openAIImageModelName}, codexImageModelName
	}
	models := []string{openAIImageModelName}
	defaultModel := ""
	for _, profile := range s.providerImageCapabilityRouteProfiles() {
		if profile.PublicModel == "" {
			continue
		}
		models = append(models, profile.PublicModel)
		if profile.RequestDefaultModel {
			defaultModel = profile.PublicModel
		}
	}
	models = uniqueStrings(models)
	if defaultModel == "" {
		defaultModel = openAIImageModelName
	}
	return models, defaultModel
}

func (s *Server) imageModelSupportsMask(model string) bool {
	profile, ok := s.providerImageCapabilityRouteProfileForModel(model)
	if !ok {
		return true
	}
	profile.withDefaults()
	return profile.RequestSupportsMask
}

func (s *Server) imageModelSupportsCount(model string, count int) bool {
	profile, ok := s.providerImageCapabilityRouteProfileForModel(model)
	if !ok {
		return count == currentImageOutputLimit
	}
	profile.withDefaults()
	return count > 0 && count <= profile.RequestMaxOutputImages && count <= currentImageOutputLimit
}

func imageJobCount(job ImageJob) int {
	if job.Count > 0 {
		return job.Count
	}
	return currentImageOutputLimit
}

func (s *Server) imageModelSupportsSize(model string, size string) bool {
	profile, ok := s.providerImageCapabilityRouteProfileForModel(model)
	if !ok {
		return validGPTImage2Size(size)
	}
	profile.withDefaults()
	if len(profile.RequestAllowedSizes) > 0 {
		return stringInList(size, profile.RequestAllowedSizes)
	}
	switch strings.TrimSpace(profile.RequestSizePolicy) {
	case imageRequestSizePolicyGPTImage2:
		return validGPTImage2Size(size)
	default:
		return false
	}
}

func (s *Server) imageModelSupportsQuality(model string, quality string) bool {
	profile, ok := s.providerImageCapabilityRouteProfileForModel(model)
	if !ok {
		return stringInList(quality, defaultImageRequestQualities())
	}
	profile.withDefaults()
	return stringInList(quality, profile.RequestAllowedQualities)
}

func (s *Server) imageModelSupportsResponseFormat(model string, responseFormat string) bool {
	profile, ok := s.providerImageCapabilityRouteProfileForModel(model)
	if !ok {
		return stringInList(responseFormat, defaultImageResponseFormats())
	}
	profile.withDefaults()
	return stringInList(responseFormat, profile.RequestAllowedFormats)
}
