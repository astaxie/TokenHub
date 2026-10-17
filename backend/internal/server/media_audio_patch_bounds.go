package server

import (
	"encoding/json"
	"mime/multipart"
	"net/http"
)

// Reapply upload limits after hooks using the same multipart encoding as the
// provider request. Counting includes boundaries and headers without copying audio.
func (m mediaRequest) validateAudioPatchBounds(model string) error {
	if !m.requiresNativeTranscription() {
		return nil
	}
	fields := make(map[string]json.RawMessage, len(m.Fields))
	for name, raw := range m.Fields {
		fields[name] = raw
	}
	fields["model"], _ = json.Marshal(model)
	parts := len(m.Files)
	for _, raw := range fields {
		values, err := mediaMultipartFieldValues(raw)
		if err != nil {
			return invalidAudioPatchBounds()
		}
		parts += len(values)
		if parts > 128 {
			return invalidAudioPatchBounds()
		}
		for _, value := range values {
			if len(value) > maxImageTextFieldBytes {
				return invalidAudioPatchBounds()
			}
		}
	}
	limit := m.maxBytes
	if limit <= 0 {
		limit = defaultMaxMultimodalRequestBytes
	}
	counter := &audioMultipartSizeCounter{remaining: limit}
	if err := writeMediaMultipart(multipart.NewWriter(counter), fields, m.Files); err != nil {
		return invalidAudioPatchBounds()
	}
	return nil
}

type audioMultipartSizeCounter struct{ remaining int64 }

func (w *audioMultipartSizeCounter) Write(data []byte) (int, error) {
	if int64(len(data)) > w.remaining {
		return 0, invalidAudioPatchBounds()
	}
	w.remaining -= int64(len(data))
	return len(data), nil
}

func invalidAudioPatchBounds() error {
	return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin exceeded transcription multipart field, part count, or request size limits")
}

func (m mediaRequest) validateTransformedAudioBounds(model string) error {
	if !m.patched {
		return nil
	}
	if err := m.validateAudioPatchBounds(model); err != nil {
		if m.guardrailMasked {
			return invalidAudioGuardrailBounds()
		}
		return err
	}
	return nil
}

func (m *mediaRequest) validateAudioGuardrailBounds() error {
	if !m.requiresNativeTranscription() {
		return nil
	}
	if err := m.validateAudioPatchBounds(m.model()); err != nil {
		return invalidAudioGuardrailBounds()
	}
	// The actual upstream model is substituted during encoding. Recheck its
	// encoded size without applying new limits to unchanged uploads.
	m.patched, m.guardrailMasked = true, true
	return nil
}

func invalidAudioGuardrailBounds() error {
	return NewHTTPError(http.StatusRequestEntityTooLarge, "request_too_large", "Content security transformation exceeded transcription multipart limits")
}
