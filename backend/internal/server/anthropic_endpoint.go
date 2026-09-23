package server

import "strings"

func anthropicEndpointURL(baseURL string, endpoint string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	endpoint = "/" + strings.TrimLeft(strings.TrimSpace(endpoint), "/")
	if strings.HasSuffix(strings.ToLower(baseURL), "/v1") && strings.HasPrefix(strings.ToLower(endpoint), "/v1/") {
		endpoint = endpoint[len("/v1"):]
	}
	return baseURL + endpoint
}
