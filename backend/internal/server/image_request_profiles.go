package server

import (
	"net/http"
	"strings"
)

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
