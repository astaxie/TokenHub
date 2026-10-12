package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode"
)

// The body ID identifies the gateway job. Expose the vendor task ID separately
// so clients can query the vendor's auxiliary models without changing that ID.
func (s *Server) writeMediaBackgroundResponseID(header http.Header, job ResponseJob) {
	id := mediaBackgroundResponseID(job.ResultJSON)
	if id == "" {
		return
	}
	for _, model := range s.store.ListModels() {
		if model.Name == job.Model {
			if modelHasMediaOutput(model) {
				header.Set("x-tokenhub-upstream-response-id", id)
				header.Add("Access-Control-Expose-Headers", "x-tokenhub-upstream-response-id")
			}
			return
		}
	}
}

func mediaBackgroundResponseID(result []byte) string {
	var envelope struct {
		ID any `json:"id"`
	}
	if decodeResponsesJSON(result, &envelope) != nil {
		return ""
	}
	var id string
	switch value := envelope.ID.(type) {
	case string:
		id = value
	case json.Number:
		id = value.String()
	default:
		return ""
	}
	if len(id) > 2048 || strings.TrimSpace(id) != id {
		return ""
	}
	for _, character := range id {
		if unicode.IsControl(character) {
			return ""
		}
	}
	return id
}
