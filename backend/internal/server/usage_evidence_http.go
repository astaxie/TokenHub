package server

import (
	"encoding/json"
	"net/http"
)

func decodeUsageBody(response *http.Response) (map[string]any, error) {
	defer response.Body.Close()
	body := map[string]any{}
	decoder := json.NewDecoder(response.Body)
	decoder.UseNumber()
	err := decoder.Decode(&body)
	return body, err
}
func withUsageResponseMetadata(usage Usage, headers http.Header, provider Provider) Usage {
	usage.Evidence = cloneUsageEvidence(evidenceForUsage(usage))
	clean := func(value string) string {
		id := boundedUsageID(value)
		if string(redactProviderErrorSecrets([]byte(id), provider)) != id {
			return ""
		}
		return id
	}
	usage.Evidence.InvocationID = clean(firstNonEmpty(headers.Get("x-request-id"), headers.Get("request-id"), headers.Get("x-oai-request-id")))
	usage.Evidence.TraceID = clean(headers.Get("cf-ray"))
	usage.Evidence.ResponseID = clean(usage.Evidence.ResponseID)
	usage.UpstreamRequestID = usage.Evidence.InvocationID
	return usage
}
