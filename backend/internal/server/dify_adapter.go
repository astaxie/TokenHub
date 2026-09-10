package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DifyAdapter exposes one Dify application as a chat-completion model. One
// provider maps to one Dify app: the provider API key is the app's Service API
// key ("app-..." from the app's API Access page) and TokenHub routes a local
// model name to that provider.
//
// The gateway is stateless toward Dify: every request starts a new Dify
// conversation, so the full OpenAI message list is flattened into the request
// and Dify's own conversation memory is not used.
//
// Provider options:
//
//	dify_app_type        "chat" (default: Chatflow/Agent/Chatbot apps via
//	                     /v1/chat-messages) or "workflow" (Workflow apps via
//	                     /v1/workflows/run)
//	dify_input_variable  workflow input variable receiving the flattened
//	                     conversation, default "query"
//	dify_output_variable workflow output variable holding the answer, default
//	                     "answer"; falls back to text/result/output or the sole
//	                     string output when those names are absent
type DifyAdapter struct {
	Client *http.Client
	// StreamClient carries no total deadline; see provider_stream_timeout.go.
	// Streaming calls fall back to Client when it is unset.
	StreamClient      *http.Client
	StreamIdleTimeout time.Duration
}

const (
	difyAppTypeOption        = "dify_app_type"
	difyInputVariableOption  = "dify_input_variable"
	difyOutputVariableOption = "dify_output_variable"

	difyAppTypeWorkflow = "workflow"

	difyDefaultInputVariable  = "query"
	difyDefaultOutputVariable = "answer"

	difyChatMessagesPath = "/v1/chat-messages"
	difyWorkflowRunPath  = "/v1/workflows/run"
	difyParametersPath   = "/v1/parameters"
)

// difyRequest is the shared request body of /v1/chat-messages and
// /v1/workflows/run; Query is only meaningful for chat apps.
type difyRequest struct {
	Inputs       map[string]any `json:"inputs"`
	Query        string         `json:"query,omitempty"`
	User         string         `json:"user"`
	ResponseMode string         `json:"response_mode"`
}

func (a DifyAdapter) Chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error) {
	resp, err := a.do(ctx, provider, req, "blocking", false)
	if err != nil {
		return nil, Usage{}, err
	}
	defer resp.Body.Close()

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, Usage{}, invalidProviderResponseError(fmt.Sprintf("dify returned an undecodable response: %v", err))
	}
	answer, usage, err := difyBlockingResult(body, provider)
	if err != nil {
		return nil, Usage{}, err
	}
	usage.Transport = "http_json"
	return map[string]any{
		"id":      NewID("chatcmpl"),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   req.Model,
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": answer,
			},
			"finish_reason": "stop",
		}},
		"usage": openAIChatUsageObject(usage),
	}, usage, nil
}

func (a DifyAdapter) ChatStream(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest, w io.Writer) (Usage, error) {
	resp, err := a.do(ctx, provider, req, "streaming", true)
	if err != nil {
		return Usage{}, err
	}
	defer resp.Body.Close()

	encoder := newOpenAIChatStreamEncoder(w, req.Model, streamUsageRequested(req))
	usage, err := streamDifyChat(resp.Body, encoder, provider)
	if err != nil {
		return usage, err
	}
	usage.Transport = "http_sse"
	return usage, nil
}

func (a DifyAdapter) Responses(ctx context.Context, provider Provider, providerModel string, req ResponsesRequest) (any, Usage, error) {
	return nil, Usage{}, NewHTTPError(http.StatusNotImplemented, "provider_capability_not_supported", "Dify does not support the Responses API")
}

func (a DifyAdapter) Embeddings(ctx context.Context, provider Provider, providerModel string, req EmbeddingsRequest) (any, Usage, error) {
	return nil, Usage{}, NewHTTPError(http.StatusNotImplemented, "provider_capability_not_supported", "Dify does not support embeddings")
}

// DefaultProbeRequest returns the empty probe: a Dify probe reads app
// parameters instead of running a completion.
func (a DifyAdapter) DefaultProbeRequest() ProviderProbeRequest {
	return ProviderProbeRequest{}
}

// Probe validates the app credentials with GET /v1/parameters, the cheapest
// authenticated Service API call. It doubles as a hint for variable mapping:
// the response carries the app's user_input_form.
func (a DifyAdapter) Probe(ctx context.Context, provider Provider, resource ProviderResource, _ ProviderProbeRequest) (ProviderProbeResult, error) {
	effective := effectiveProviderResourceConfig(provider, &resource)
	if strings.TrimSpace(effective.BaseURL) == "" {
		return ProviderProbeResult{}, newProviderMisconfigured("Dify provider base_url is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, joinURL(difyBaseURL(effective.BaseURL), difyParametersPath), nil)
	if err != nil {
		return ProviderProbeResult{}, err
	}
	if effective.APIKey != "" {
		req.Header.Set("authorization", "Bearer "+effective.APIKey)
	}
	applyProviderHeaders(req.Header, effective.Headers)

	startedAt := time.Now()
	resp, err := sendUpstream(a.Client, a.StreamClient, a.StreamIdleTimeout, req, false)
	if err != nil {
		return ProviderProbeResult{}, err
	}
	defer resp.Body.Close()
	if err := checkProviderResponseForProvider(resp, effective); err != nil {
		return ProviderProbeResult{}, err
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return ProviderProbeResult{}, invalidProviderResponseError(fmt.Sprintf("dify returned an undecodable parameters response: %v", err))
	}
	return ProviderProbeResult{
		ResourceID: resource.ID,
		LatencyMS:  time.Since(startedAt).Milliseconds(),
		Response:   body,
	}, nil
}

func (a DifyAdapter) do(ctx context.Context, provider Provider, req ChatCompletionRequest, mode string, stream bool) (*http.Response, error) {
	if strings.TrimSpace(provider.BaseURL) == "" {
		return nil, newProviderMisconfigured("Dify provider base_url is required")
	}
	payload := difyUpstreamPayload(provider, req, mode)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	upstream, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL(difyBaseURL(provider.BaseURL), difyEndpoint(provider)), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	upstream.Header.Set("content-type", "application/json")
	if provider.APIKey != "" {
		upstream.Header.Set("authorization", "Bearer "+provider.APIKey)
	}
	applyProviderHeaders(upstream.Header, provider.Headers)

	resp, err := sendUpstream(a.Client, a.StreamClient, a.StreamIdleTimeout, upstream, stream)
	if err != nil {
		return nil, err
	}
	if err := checkProviderResponseForProvider(resp, provider); err != nil {
		return nil, err
	}
	return resp, nil
}

// difyUpstreamPayload maps an OpenAI chat request onto a Dify app call. The
// whole conversation is flattened: without a Dify conversation_id each request
// is a fresh conversation, so earlier turns must travel in the payload.
func difyUpstreamPayload(provider Provider, req ChatCompletionRequest, mode string) difyRequest {
	user := "anonymous"
	if value, ok := req.User.(string); ok && strings.TrimSpace(value) != "" {
		user = strings.TrimSpace(value)
	}
	text := ChatPromptText(req.Messages)
	payload := difyRequest{User: user, ResponseMode: mode}
	if difyWorkflowApp(provider) {
		variable := strings.TrimSpace(provider.Options[difyInputVariableOption])
		if variable == "" {
			variable = difyDefaultInputVariable
		}
		payload.Inputs = map[string]any{variable: text}
	} else {
		payload.Inputs = map[string]any{}
		payload.Query = text
	}
	return payload
}

func difyEndpoint(provider Provider) string {
	if difyWorkflowApp(provider) {
		return difyWorkflowRunPath
	}
	return difyChatMessagesPath
}

func difyWorkflowApp(provider Provider) bool {
	return strings.EqualFold(strings.TrimSpace(provider.Options[difyAppTypeOption]), difyAppTypeWorkflow)
}

// difyBaseURL accepts the Dify instance root with or without a trailing /v1
// because the endpoints below already carry their own /v1 prefix.
func difyBaseURL(raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if strings.HasSuffix(strings.ToLower(trimmed), "/v1") {
		trimmed = trimmed[:len(trimmed)-len("/v1")]
	}
	return trimmed
}

// difyBlockingResult extracts the answer text and usage from a blocking
// response, whose shape differs per app type: chat apps answer at the top
// level with metadata.usage, workflow apps under data with only total_tokens.
func difyBlockingResult(body map[string]any, provider Provider) (string, Usage, error) {
	if difyWorkflowApp(provider) {
		data, _ := body["data"].(map[string]any)
		if data == nil {
			return "", Usage{}, invalidProviderResponseError("dify workflow response is missing data")
		}
		if status, _ := data["status"].(string); status == "failed" {
			return "", Usage{}, difyWorkflowFailure(data["error"], provider)
		}
		outputs, _ := data["outputs"].(map[string]any)
		answer, err := difyOutputText(outputs, provider)
		if err != nil {
			return "", Usage{}, err
		}
		return answer, Usage{TotalTokens: int64FromAny(data["total_tokens"])}, nil
	}
	answer, _ := body["answer"].(string)
	usage := Usage{}
	if metadata, ok := body["metadata"].(map[string]any); ok {
		if reported, ok := metadata["usage"].(map[string]any); ok {
			usage = usageFromMap(map[string]any{"usage": reported})
		}
	}
	return answer, usage, nil
}

// difyOutputText resolves a workflow's answer from its output variables. Their
// names are workflow-defined, so an explicit dify_output_variable wins, the
// common names are tried, and a single string output is accepted as-is.
func difyOutputText(outputs map[string]any, provider Provider) (string, error) {
	if configured := strings.TrimSpace(provider.Options[difyOutputVariableOption]); configured != "" {
		if value, ok := outputs[configured].(string); ok {
			return value, nil
		}
	}
	for _, name := range []string{difyDefaultOutputVariable, "text", "result", "output"} {
		if value, ok := outputs[name].(string); ok {
			return value, nil
		}
	}
	var sole string
	found := false
	for _, value := range outputs {
		text, ok := value.(string)
		if !ok {
			continue
		}
		if found {
			found = false
			break
		}
		sole, found = text, true
	}
	if found {
		return sole, nil
	}
	return "", invalidProviderResponseError(
		"dify workflow finished without a usable string output variable; configure dify_output_variable to name one")
}

func difyWorkflowFailure(raw any, provider Provider) error {
	detail := "workflow failed"
	if message := difyErrorMessage(raw); message != "" {
		detail = message
	}
	return NewHTTPError(http.StatusBadGateway, "provider_upstream_error",
		string(redactProviderErrorSecrets([]byte(fmt.Sprintf("dify workflow failed: %s", detail)), provider)))
}

// difyErrorMessage digs a human-readable message out of Dify's error shapes,
// which vary between a bare string and an object with message/code.
func difyErrorMessage(raw any) string {
	switch typed := raw.(type) {
	case string:
		return typed
	case map[string]any:
		if message, ok := typed["message"].(string); ok && message != "" {
			return message
		}
	}
	return ""
}

// difyStreamDecoder translates a Dify SSE stream into OpenAI chat chunks. Dify
// frames put the event type inside the JSON payload: chat apps carry their
// fields at the top level (message/message_end) while workflow apps nest them
// under data (text_chunk/workflow_finished).
type difyStreamDecoder struct {
	encoder  *openAIChatStreamEncoder
	provider Provider
	workflow bool
	usage    Usage
	terminal bool
}

func streamDifyChat(body io.Reader, encoder *openAIChatStreamEncoder, provider Provider) (Usage, error) {
	decoder := &difyStreamDecoder{encoder: encoder, provider: provider, workflow: difyWorkflowApp(provider)}
	events := newSSEDecoder(body)
	for {
		event, err := events.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return decoder.abort(err)
		}
		payload, err := decodeSSEData(event)
		if err != nil {
			return decoder.abort(err)
		}
		if payload == nil {
			continue
		}
		done, err := decoder.consume(payload)
		if err != nil {
			return decoder.abort(err)
		}
		if done {
			break
		}
	}
	if !decoder.terminal {
		// A Dify stream always closes with message_end or workflow_finished.
		// EOF before that means the connection dropped mid-run; finalizing
		// would hand the client a silently truncated answer.
		return decoder.abort(invalidProviderResponseError("dify closed the stream before completing the run"))
	}
	if err := encoder.Finalize("stop", decoder.usage); err != nil {
		return decoder.usage, err
	}
	return decoder.usage, nil
}

func (d *difyStreamDecoder) abort(err error) (Usage, error) {
	d.encoder.Abort()
	return d.usage, err
}

// consume handles one decoded SSE payload. The boolean result reports stream
// completion.
func (d *difyStreamDecoder) consume(payload map[string]any) (bool, error) {
	switch event, _ := payload["event"].(string); event {
	case "message":
		// Chat apps stream the visible answer as top-level answer deltas.
		answer, _ := payload["answer"].(string)
		return false, d.encoder.EmitText(answer)
	case "text_chunk":
		// Workflow apps stream LLM output under data.text.
		data, _ := payload["data"].(map[string]any)
		text, _ := data["text"].(string)
		return false, d.encoder.EmitText(text)
	case "message_end":
		if metadata, ok := payload["metadata"].(map[string]any); ok {
			if reported, ok := metadata["usage"].(map[string]any); ok {
				d.usage = usageFromMap(map[string]any{"usage": reported})
			}
		}
		d.terminal = true
		return true, nil
	case "workflow_finished":
		data, _ := payload["data"].(map[string]any)
		if status, _ := data["status"].(string); status == "failed" {
			return false, difyWorkflowFailure(data["error"], d.provider)
		}
		// Workflows report no prompt/completion split, only the run total.
		d.usage.TotalTokens = int64FromAny(data["total_tokens"])
		d.terminal = true
		return true, nil
	case "error":
		message := firstNonEmpty(difyErrorMessage(payload["message"]), difyErrorMessage(payload["data"]), "unknown error")
		return false, NewHTTPError(http.StatusBadGateway, "provider_stream_error",
			fmt.Sprintf("dify stream error: %s", message))
	default:
		// workflow_started, node lifecycle, ping, agent_thought, tts and file
		// events carry no OpenAI-equivalent content.
		return false, nil
	}
}

// difyConnectionTestCatalog backs the admin connection test for dify
// providers. Dify apps have no /models endpoint: the parameters probe is the
// credential check, and the inventory is whatever custom models the
// administrator declared.
func (s *Server) difyConnectionTestCatalog(ctx context.Context, req ProviderCreateRequest) (ProviderCatalogEntry, error) {
	adapter, ok := resolveTypedAdapter[DifyAdapter](s.adapterRegistry, ProviderDify)
	if !ok {
		return ProviderCatalogEntry{}, NewHTTPError(http.StatusInternalServerError, "provider_adapter_missing", "Dify adapter is unavailable")
	}
	provider := Provider{Name: req.Name, Type: ProviderDify, BaseURL: req.BaseURL, APIKey: req.APIKey, Headers: req.Headers, SensitiveHeaders: req.SensitiveHeaders, Options: req.Options}
	if _, err := adapter.Probe(ctx, provider, ProviderResource{}, adapter.DefaultProbeRequest()); err != nil {
		return ProviderCatalogEntry{}, err
	}
	return customProviderCatalogFromModelsWithType(req.CustomModels, req.ModelCategory, ProviderDify), nil
}
