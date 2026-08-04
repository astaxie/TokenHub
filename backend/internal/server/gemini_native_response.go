package server

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func codexResponsesToGemini(body map[string]any, model string, usage Usage, reverseNames map[string]string) (map[string]any, error) {
	parts := make([]any, 0, 4)
	lastSignature := ""
	hasToolCalls := false
	outputs, _ := anySlice(body["output"])
	for _, rawItem := range outputs {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		switch item["type"] {
		case "reasoning":
			summary := codexReasoningText(item)
			encrypted, _ := item["encrypted_content"].(string)
			if encrypted != "" {
				lastSignature = encodeProviderSignature(codexSignatureProvider, encrypted)
			}
			if summary != "" {
				part := map[string]any{"text": summary, "thought": true}
				if lastSignature != "" {
					part["thoughtSignature"] = lastSignature
				}
				parts = append(parts, part)
			}
		case "message":
			content, _ := anySlice(item["content"])
			for _, rawPart := range content {
				part, _ := rawPart.(map[string]any)
				if text, ok := codexGeminiOutputText(part); ok {
					parts = append(parts, map[string]any{"text": text})
				}
			}
		case "function_call":
			callID, name, arguments, err := codexFunctionCall(item)
			if err != nil {
				return nil, err
			}
			hasToolCalls = true
			part := map[string]any{"functionCall": map[string]any{
				"id": codexShortIdentifier(callID), "name": codexRestoreToolName(reverseNames, name), "args": arguments,
			}}
			if lastSignature != "" {
				part["thoughtSignature"] = lastSignature
			}
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		parts = append(parts, map[string]any{"text": codexResponseOutputText(body)})
	}
	id, _ := body["id"].(string)
	if id == "" {
		id = NewID("resp")
	}
	return map[string]any{
		"candidates": []any{map[string]any{
			"content":      map[string]any{"role": "model", "parts": parts},
			"finishReason": codexGeminiFinishReason(body, hasToolCalls),
			"index":        0,
		}},
		"usageMetadata": geminiUsageMetadata(usage),
		"modelVersion":  model,
		"responseId":    id,
	}, nil
}

func codexGeminiOutputText(part map[string]any) (string, bool) {
	switch part["type"] {
	case "output_text":
		text, ok := part["text"].(string)
		return text, ok
	case "refusal":
		text, ok := part["refusal"].(string)
		return text, ok
	default:
		return "", false
	}
}

func codexGeminiFinishReason(body map[string]any, hasToolCalls bool) string {
	if hasToolCalls {
		return "STOP"
	}
	switch codexResponseIncompleteReason(body) {
	case "max_output_tokens", "max_tokens":
		return "MAX_TOKENS"
	case "content_filter", "safety":
		return "SAFETY"
	default:
		return "STOP"
	}
}

func geminiUsageMetadata(usage Usage) map[string]any {
	metadata := map[string]any{
		"promptTokenCount":     usage.PromptTokens,
		"candidatesTokenCount": usage.CompletionTokens,
		"totalTokenCount":      usage.TotalTokens,
	}
	if usage.CachedInputTokens > 0 {
		metadata["cachedContentTokenCount"] = usage.CachedInputTokens
	}
	if usage.ReasoningOutputTokens > 0 {
		metadata["thoughtsTokenCount"] = usage.ReasoningOutputTokens
	}
	return metadata
}

type codexGeminiStreamTool struct {
	id        string
	name      string
	arguments strings.Builder
}

type codexGeminiStreamSink struct {
	writer       io.Writer
	model        string
	reverseNames map[string]string
	responseID   string
	signature    string
	tools        map[string]*codexGeminiStreamTool
	wroteContent bool
}

func newCodexGeminiStreamSink(writer io.Writer, model string, reverseNames map[string]string) *codexGeminiStreamSink {
	return &codexGeminiStreamSink{writer: writer, model: model, reverseNames: reverseNames, tools: map[string]*codexGeminiStreamTool{}}
}

func (s *codexGeminiStreamSink) Start(response map[string]any) error {
	s.responseID, _ = response["id"].(string)
	return nil
}

func (s *codexGeminiStreamSink) TextDelta(delta string) error {
	if delta == "" {
		return nil
	}
	s.wroteContent = true
	return s.emit([]any{map[string]any{"text": delta}}, "", nil)
}

func (s *codexGeminiStreamSink) TextDone() error { return nil }

func (s *codexGeminiStreamSink) ReasoningDelta(delta string) error {
	if delta == "" {
		return nil
	}
	s.wroteContent = true
	return s.emit([]any{map[string]any{"text": delta, "thought": true}}, "", nil)
}

func (s *codexGeminiStreamSink) ReasoningDone(signature string) error {
	if signature != "" {
		s.signature = encodeProviderSignature(codexSignatureProvider, signature)
	}
	return nil
}

func (s *codexGeminiStreamSink) ToolStart(key string, id string, name string) error {
	s.tools[key] = &codexGeminiStreamTool{id: codexShortIdentifier(id), name: codexRestoreToolName(s.reverseNames, name)}
	return nil
}

func (s *codexGeminiStreamSink) ToolArguments(key string, delta string) error {
	tool := s.tools[key]
	if tool == nil {
		return fmt.Errorf("gemini stream received arguments for unknown tool %q", key)
	}
	tool.arguments.WriteString(delta)
	return nil
}

func (s *codexGeminiStreamSink) ToolDone(key string) error {
	tool := s.tools[key]
	if tool == nil {
		return fmt.Errorf("gemini stream completed unknown tool %q", key)
	}
	arguments := map[string]any{}
	if raw := strings.TrimSpace(tool.arguments.String()); raw != "" {
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&arguments); err != nil {
			return NewHTTPError(502, "provider_invalid_response", "Codex function arguments are not valid JSON")
		}
	}
	part := map[string]any{"functionCall": map[string]any{"id": tool.id, "name": tool.name, "args": arguments}}
	if s.signature != "" {
		part["thoughtSignature"] = s.signature
	}
	s.wroteContent = true
	delete(s.tools, key)
	return s.emit([]any{part}, "", nil)
}

func (s *codexGeminiStreamSink) Finalize(response map[string]any, usage Usage, hasToolCalls bool) error {
	if id, _ := response["id"].(string); id != "" {
		s.responseID = id
	}
	parts := []any(nil)
	if !s.wroteContent {
		parts = []any{map[string]any{"text": codexResponseOutputText(response)}}
	}
	return s.emit(parts, codexGeminiFinishReason(response, hasToolCalls), geminiUsageMetadata(usage))
}

func (s *codexGeminiStreamSink) Abort() {}

func (s *codexGeminiStreamSink) emit(parts []any, finishReason string, usage map[string]any) error {
	if s.responseID == "" {
		s.responseID = NewID("resp")
	}
	candidate := map[string]any{"index": 0}
	if len(parts) > 0 {
		candidate["content"] = map[string]any{"role": "model", "parts": parts}
	}
	if finishReason != "" {
		candidate["finishReason"] = finishReason
	}
	payload := map[string]any{
		"candidates":   []any{candidate},
		"modelVersion": s.model,
		"responseId":   s.responseID,
	}
	if usage != nil {
		payload["usageMetadata"] = usage
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(s.writer, "data: %s\n\n", encoded)
	return err
}
