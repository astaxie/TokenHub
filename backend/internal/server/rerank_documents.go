package server

import (
	"bytes"
	"encoding/json"
	"strings"
)

const (
	maxRerankDocuments       = 2048
	maxRerankDocumentBytes   = 64 << 10
	maxRerankDocumentIDBytes = 256
)

type rerankRequestFields RerankRequest

// Keep text and IDs separate so every native provider receives text-only inputs.
func (r *RerankRequest) UnmarshalJSON(data []byte) error {
	var fields rerankRequestFields
	wire := struct {
		*rerankRequestFields
		Documents []json.RawMessage `json:"documents"`
	}{rerankRequestFields: &fields}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	fields.Documents = make([]string, len(wire.Documents))
	for index, raw := range wire.Documents {
		raw = bytes.TrimSpace(raw)
		if len(raw) > 0 && raw[0] == '"' {
			if err := json.Unmarshal(raw, &fields.Documents[index]); err != nil {
				return err
			}
			continue
		}
		var document struct {
			ID   *string `json:"id"`
			Text *string `json:"text"`
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&document); err != nil || document.ID == nil || document.Text == nil || strings.TrimSpace(*document.ID) == "" {
			return NewHTTPError(400, "invalid_rerank_request", "Documents must be text strings or objects containing a nonblank string id and string text")
		}
		if fields.DocumentIDs == nil {
			fields.DocumentIDs = make([]string, len(wire.Documents))
		}
		fields.DocumentIDs[index] = *document.ID
		fields.Documents[index] = *document.Text
	}
	*r = RerankRequest(fields)
	return nil
}

func (r RerankRequest) MarshalJSON() ([]byte, error) {
	if len(r.DocumentIDs) == 0 {
		return json.Marshal(rerankRequestFields(r))
	}
	documents := make([]any, len(r.Documents))
	for index, text := range r.Documents {
		documents[index] = text
		if id := r.documentID(index); id != "" {
			documents[index] = map[string]string{"id": id, "text": text}
		}
	}
	return json.Marshal(struct {
		rerankRequestFields
		Documents []any `json:"documents"`
	}{rerankRequestFields: rerankRequestFields(r), Documents: documents})
}

func (r RerankRequest) documentID(index int) string {
	if index < len(r.DocumentIDs) {
		return r.DocumentIDs[index]
	}
	return ""
}
