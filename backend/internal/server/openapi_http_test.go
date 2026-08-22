package server

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

//go:embed openapi/openapi-3.1.schema.json
var openAPI31Schema []byte

func TestOpenAPIEndpointsServeRuntimeServerAndSelfHostedDocs(t *testing.T) {
	app := NewWithConfig(NewMemoryStore(), Config{
		AppVersion:    "9.8.7-test",
		PublicBaseURL: "https://gateway.example.com/base",
	}).Handler()

	openapi := httptest.NewRecorder()
	app.ServeHTTP(openapi, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if openapi.Code != http.StatusOK {
		t.Fatalf("openapi status=%d body=%s", openapi.Code, openapi.Body.String())
	}
	if contentType := openapi.Header().Get("Content-Type"); !strings.Contains(contentType, "application/json") {
		t.Fatalf("openapi content-type=%q", contentType)
	}
	var document map[string]any
	if err := json.Unmarshal(openapi.Body.Bytes(), &document); err != nil {
		t.Fatalf("openapi json: %v", err)
	}
	servers := document["servers"].([]any)
	if got := servers[0].(map[string]any)["url"]; got != "https://gateway.example.com/base" {
		t.Fatalf("server url=%v", got)
	}
	if got := document["info"].(map[string]any)["version"]; got != "9.8.7-test" {
		t.Fatalf("version=%v", got)
	}

	docs := httptest.NewRecorder()
	app.ServeHTTP(docs, httptest.NewRequest(http.MethodGet, "/docs", nil))
	if docs.Code != http.StatusOK {
		t.Fatalf("docs status=%d body=%s", docs.Code, docs.Body.String())
	}
	body := docs.Body.String()
	for _, forbidden := range []string{"cdn.jsdelivr", "unpkg.com", "cdnjs.cloudflare", "localStorage", "sessionStorage"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("docs page contains forbidden external/persistent reference %q", forbidden)
		}
	}
	for _, want := range []string{
		"TokenHub API Reference",
		"/base/openapi.json",
		"/base/docs/swagger-ui.css",
		"/base/docs/swagger-ui-bundle.js",
		"Swagger UI",
		"persistAuthorization: false",
		"requestSnippetsEnabled: false",
		"showMutatedRequest: false",
		"validatorUrl: null",
		"data-tokenhub-secret",
		`input.type = "password"`,
		"x-tokenhub-interactive",
		"kept in memory",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("docs page missing %q", want)
		}
	}
	if !strings.Contains(body, `path.startsWith(basePath + "/")`) || !strings.Contains(body, `path = path.slice(basePath.length)`) {
		t.Fatal("docs page must preserve TOKENHUB_PUBLIC_BASE_URL path prefixes in browser try requests")
	}
	for _, asset := range []struct {
		path        string
		contentType string
		needle      string
	}{
		{path: "/docs/swagger-ui.css", contentType: "text/css", needle: "swagger-ui"},
		{path: "/docs/swagger-ui-bundle.js", contentType: "application/javascript", needle: "SwaggerUIBundle"},
		{path: "/docs/swagger-ui-dist.LICENSE", contentType: "text/plain", needle: "Apache License"},
	} {
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, asset.path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d", asset.path, response.Code)
		}
		if contentType := response.Header().Get("Content-Type"); !strings.Contains(contentType, asset.contentType) {
			t.Fatalf("%s content-type=%q", asset.path, contentType)
		}
		if !strings.Contains(response.Body.String(), asset.needle) {
			t.Fatalf("%s missing %q", asset.path, asset.needle)
		}
	}
}

func TestOpenAPIUsesTrustedForwardedOriginWhenPublicBaseURLIsUnset(t *testing.T) {
	app := NewWithConfig(NewMemoryStore(), Config{
		TrustedProxyCIDRs: []string{"192.0.2.10/32"},
	}).Handler()
	request := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	request.RemoteAddr = "192.0.2.10:1234"
	request.Host = "internal.invalid"
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-Host", "public.tokenhub.example")

	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("openapi status=%d body=%s", response.Code, response.Body.String())
	}
	var document map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	servers := document["servers"].([]any)
	if got := servers[0].(map[string]any)["url"]; got != "https://public.tokenhub.example" {
		t.Fatalf("server url=%v", got)
	}
}

func TestGatewayOpenAPISpecIsValidAndCoversPublicGatewayRoutes(t *testing.T) {
	assertNoDuplicateYAMLKeys(t, gatewayOpenAPIYAML)
	var document map[string]any
	if err := yaml.Unmarshal(gatewayOpenAPIYAML, &document); err != nil {
		t.Fatalf("openapi yaml: %v", err)
	}
	if got := document["openapi"]; got != "3.1.0" {
		t.Fatalf("openapi version=%v", got)
	}
	assertOpenAPI31SchemaValid(t, document)
	assertGatewaySecuritySchemes(t, document)
	assertUniqueOperationIDs(t, document)
	assertLocalRefsResolve(t, document)
	assertMediaTypeExamplesValidate(t, document)
	assertRepresentativeGatewayBehaviors(t, document)
	assertNonGatewayRoutesStayOutOfPublicGatewaySpec(t, document)
	assertCommonGatewayHeadersAndErrors(t, document)

	documented := documentedGatewayOperations(document)
	registered := registeredGatewayOperations(t)
	if diff := operationDiff(registered, documented); len(diff) > 0 {
		t.Fatalf("registered public gateway routes missing from OpenAPI: %s", strings.Join(diff, ", "))
	}
	if diff := operationDiff(documented, registered); len(diff) > 0 {
		t.Fatalf("OpenAPI documents routes not registered as public gateway routes: %s", strings.Join(diff, ", "))
	}
	for operation := range documented {
		if !strings.HasPrefix(operation.Path, "/v1/") && operation.Path != "/v1/models" &&
			!strings.HasPrefix(operation.Path, "/v1beta/") && operation.Path != "/v1beta/models" {
			t.Fatalf("public gateway spec includes non-gateway route: %s %s", operation.Method, operation.Path)
		}
	}
}

func assertNoDuplicateYAMLKeys(t *testing.T, raw []byte) {
	t.Helper()
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		t.Fatalf("openapi yaml: %v", err)
	}
	var visit func(*yaml.Node, string)
	visit = func(node *yaml.Node, path string) {
		if node.Kind == yaml.MappingNode {
			seen := make(map[string]int)
			for i := 0; i+1 < len(node.Content); i += 2 {
				key := node.Content[i]
				value := node.Content[i+1]
				if line, ok := seen[key.Value]; ok {
					t.Fatalf("duplicate OpenAPI YAML key %q at %s line %d; first seen on line %d", key.Value, path, key.Line, line)
				}
				seen[key.Value] = key.Line
				nextPath := path + "/" + key.Value
				if path == "" {
					nextPath = key.Value
				}
				visit(value, nextPath)
			}
			return
		}
		for _, child := range node.Content {
			visit(child, path)
		}
	}
	visit(&root, "")
}

func assertOpenAPI31SchemaValid(t *testing.T, document map[string]any) {
	t.Helper()
	var schema any
	if err := json.Unmarshal(openAPI31Schema, &schema); err != nil {
		t.Fatalf("openapi 3.1 schema json: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("https://tokenhub.local/openapi-3.1.schema.json", schema); err != nil {
		t.Fatalf("add openapi 3.1 schema: %v", err)
	}
	compiled, err := compiler.Compile("https://tokenhub.local/openapi-3.1.schema.json")
	if err != nil {
		t.Fatalf("compile openapi 3.1 schema: %v", err)
	}
	if err := compiled.Validate(jsonRoundTrip(t, document)); err != nil {
		t.Fatalf("openapi 3.1 schema validation: %v", err)
	}
}

func assertMediaTypeExamplesValidate(t *testing.T, document map[string]any) {
	t.Helper()
	paths := asMap(t, document["paths"], "paths")
	for path, rawItem := range paths {
		item := asMap(t, rawItem, "paths."+path)
		for method, rawOperation := range item {
			switch method {
			case "get", "post", "put", "patch", "delete":
			default:
				continue
			}
			operation := asMap(t, rawOperation, method+" "+path)
			validateExamplesInContent(t, document, operation, path, strings.ToUpper(method)+" "+path+" requestBody", "requestBody", "content")
			responses, _ := operation["responses"].(map[string]any)
			for status, rawResponse := range responses {
				response := resolveOpenAPIRef(t, document, rawResponse, strings.ToUpper(method)+" "+path+" response "+status)
				validateExamplesInContent(t, document, response, path, strings.ToUpper(method)+" "+path+" response "+status, "content")
			}
		}
	}
}

func validateExamplesInContent(t *testing.T, document map[string]any, owner map[string]any, operationPath string, label string, keys ...string) {
	t.Helper()
	current := any(owner)
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return
		}
		current = object[key]
	}
	content, ok := current.(map[string]any)
	if !ok {
		return
	}
	for mediaType, rawMedia := range content {
		media := asMap(t, rawMedia, label+" "+mediaType)
		examples, ok := media["examples"].(map[string]any)
		if !ok {
			continue
		}
		schema, ok := media["schema"].(map[string]any)
		if !ok {
			t.Fatalf("%s %s examples must declare a schema", label, mediaType)
		}
		for name, rawExample := range examples {
			example := resolveOpenAPIRef(t, document, rawExample, label+" "+mediaType+" example "+name)
			value, ok := example["value"]
			if !ok {
				t.Fatalf("%s %s example %q has no value", label, mediaType, name)
			}
			if strings.HasPrefix(mediaType, "text/event-stream") || operationPath == "/v1/image-assets/{asset_id}/content" {
				continue
			}
			assertExampleMatchesSchema(t, document, schema, value, label+" "+mediaType+" example "+name)
		}
	}
}

func assertExampleMatchesSchema(t *testing.T, document map[string]any, schema map[string]any, value any, label string) {
	t.Helper()
	components := asMap(t, document["components"], "components")
	schemas := asMap(t, components["schemas"], "components.schemas")
	testSchema := rewriteSchemaRefs(jsonRoundTrip(t, schema))
	if object, ok := testSchema.(map[string]any); ok {
		object["$schema"] = "https://json-schema.org/draft/2020-12/schema"
		object["$defs"] = rewriteSchemaRefs(jsonRoundTrip(t, schemas))
	}
	compiler := jsonschema.NewCompiler()
	resourceID := "https://tokenhub.local/example-schema/" + sanitizeSchemaResourceName(label) + ".json"
	if err := compiler.AddResource(resourceID, testSchema); err != nil {
		t.Fatalf("%s add schema: %v", label, err)
	}
	compiled, err := compiler.Compile(resourceID)
	if err != nil {
		t.Fatalf("%s compile schema: %v", label, err)
	}
	if err := compiled.Validate(jsonRoundTrip(t, value)); err != nil {
		t.Fatalf("%s does not match schema: %v", label, err)
	}
}

func assertRepresentativeGatewayBehaviors(t *testing.T, document map[string]any) {
	t.Helper()
	requireExampleValueContains(t, document, "/v1/chat/completions", "post", "200", "text/event-stream", "chunk", "data:")
	requireExampleValueContains(t, document, "/v1/responses", "post", "200", "text/event-stream", "delta", "event: response.output_text.delta")
	requireRequestExampleField(t, document, "/v1/responses", "post", "background", "background", true)
	requireSchemaEnum(t, document, "ResponseJob", "status", []string{"queued", "in_progress", "completed", "failed", "cancelled"})
	requireSchemaProperty(t, document, "ResponseJob", "object")
	requireSchemaProperty(t, document, "ResponseJob", "background")
	requireSchemaProperty(t, document, "ResponseJob", "output")
	requireSchemaNoProperty(t, document, "ResponseJob", "response")
	requireRequestContentType(t, document, "/v1/images/edits", "post", "multipart/form-data")
	requireRequestExampleFieldForMedia(t, document, "/v1/images/edits", "post", "multipart/form-data", "editWithMask", "image", "@portrait.png")
	requireOperationParameter(t, document, "/v1/images/generations", "post", "header", "Prefer")
	requireOperationParameter(t, document, "/v1/images/generations", "post", "header", "x-tokenhub-async")
	requireOperationParameter(t, document, "/v1/images/edits", "post", "header", "Prefer")
	requireOperationParameter(t, document, "/v1/images/edits", "post", "header", "x-tokenhub-async")
	requireSchemaNoProperty(t, document, "ImageGenerationRequest", "background")
	requireSchemaNoProperty(t, document, "ImageEditRequest", "background")
	requireSchemaEnum(t, document, "ImageJob", "status", []string{"queued", "running", "completed", "failed"})
	requireSchemaProperty(t, document, "ImageJob", "data")
	requireSchemaProperty(t, document, "ImageJob", "input_image_count")
	requireSchemaNoProperty(t, document, "ImageJob", "result")
	requireResponseContentType(t, document, "/v1/images/generations", "post", "202", "application/json")
	requireResponseHeader(t, document, "/v1/images/generations", "post", "202", "location")
	requireResponseRateLimitHeaders(t, document, "/v1/images/generations", "post", "202")
	requireResponseContentType(t, document, "/v1/images/edits", "post", "202", "application/json")
	requireResponseHeader(t, document, "/v1/images/edits", "post", "202", "location")
	requireResponseRateLimitHeaders(t, document, "/v1/images/edits", "post", "202")
	requireResponseRef(t, document, "/v1/images/edits", "post", "501", "#/components/responses/ImageMaskNotSupported")
	requireInteractiveFlag(t, document, "/v1/images/edits", "post", false)
	requireResponseContentType(t, document, "/v1/image-assets/{asset_id}/content", "get", "200", "application/octet-stream")
	requireResponseExampleValueContains(t, document, "/v1/image-assets/{asset_id}/content", "get", "200", "application/octet-stream", "pngBytes", "binary image bytes")
	requireOperationSecurityEmpty(t, document, "/v1/image-assets/{asset_id}/content", "get")
	requireNoResponseHeader(t, document, "/v1/image-assets/{asset_id}/content", "get", "200", "x-request-id")
	requireNoResponseHeader(t, document, "/v1/image-assets/{asset_id}/content", "get", "200", "x-ratelimit-limit-requests")
	requireInteractiveFlag(t, document, "/v1/image-assets/{asset_id}/content", "get", false)
	requireResponseContentType(t, document, "/v1beta/models/{model}:streamGenerateContent", "post", "200", "text/event-stream")
	requireNoResponseContentType(t, document, "/v1beta/models/{model}:streamGenerateContent", "post", "200", "application/json")
	requireExampleValueContains(t, document, "/v1/messages", "post", "200", "text/event-stream", "messageStart", "event: message_start")
	requireExampleValueContains(t, document, "/v1beta/models/{model}:streamGenerateContent", "post", "200", "text/event-stream", "candidate", `"candidates"`)
	requireResponseRef(t, document, "/v1/messages", "post", "403", "#/components/responses/AnthropicModelNotAllowed")
	requireResponseRef(t, document, "/v1/messages", "post", "501", "#/components/responses/AnthropicProviderCapabilityNotSupported")
	requireResponseRef(t, document, "/v1/messages", "post", "502", "#/components/responses/AnthropicProviderError")
	requireOperationParameter(t, document, "/v1/messages", "post", "header", "anthropic-version")
	requireOperationParameter(t, document, "/v1/messages", "post", "header", "anthropic-beta")
	requireArrayItemsRef(t, document, "AnthropicMessagesRequest", "messages", "#/components/schemas/AnthropicMessage")
	requireSchemaEnum(t, document, "AnthropicMessage", "role", []string{"user", "assistant", "system"})
	requireSchemaDescriptionContains(t, document, "AnthropicMessage", "mid-conversation-system-2026-04-07")
	requireRequestSchemaRef(t, document, "/v1/messages/count_tokens", "post", "#/components/schemas/AnthropicCountTokensRequest")
	requireSchemaRequired(t, document, "AnthropicCountTokensRequest", []string{"model", "messages"})
	requireResponseRef(t, document, "/v1/messages/count_tokens", "post", "400", "#/components/responses/AnthropicBadRequest")
	requireResponseRef(t, document, "/v1/messages/count_tokens", "post", "403", "#/components/responses/AnthropicModelNotAllowed")
	requireOperationParameter(t, document, "/v1/messages/count_tokens", "post", "header", "anthropic-version")
	requireOperationParameter(t, document, "/v1/messages/count_tokens", "post", "header", "anthropic-beta")
	requireArrayItemsRef(t, document, "AnthropicCountTokensRequest", "messages", "#/components/schemas/AnthropicMessage")
	requireSchemaRequired(t, document, "ChatMessage", []string{"role"})
	requireSchemaEnum(t, document, "ChatCompletionRequest", "reasoning_effort", []string{"minimal", "low", "medium", "high", "xhigh", "max"})
	requireSchemaProperty(t, document, "ResponsesRequest", "max_output_tokens")
	requireSchemaProperty(t, document, "ResponsesRequest", "temperature")
	requireSchemaProperty(t, document, "ResponsesRequest", "instructions")
	requireSchemaProperty(t, document, "ResponsesRequest", "store")
	requireSchemaProperty(t, document, "ResponsesRequest", "reasoning")
	requireSchemaProperty(t, document, "ResponsesRequest", "service_tier")
	requireOneOfContainsRef(t, document, "ResponsesRequest", "input", "#/components/schemas/ResponseInputItem")
	requireSchemaEnum(t, document, "ResponsesReasoning", "effort", []string{"minimal", "low", "medium", "high", "xhigh", "max"})
	requireSchemaRequired(t, document, "ResponseTool", []string{"type"})
	requireSchemaPropertyNoConst(t, document, "ResponseTool", "type")
	requireArrayItemsRef(t, document, "ResponsesResponse", "output", "#/components/schemas/ResponseOutputItem")
	requireArrayItemsRef(t, document, "ChatCompletionResponse", "choices", "#/components/schemas/ChatCompletionChoice")
}

func assertNonGatewayRoutesStayOutOfPublicGatewaySpec(t *testing.T, document map[string]any) {
	t.Helper()
	paths := asMap(t, document["paths"], "paths")
	for _, path := range []string{
		"/api/v1/analytics/token-costs",
		"/api/admin/auth/login",
		"/healthz",
		"/metrics",
	} {
		if _, ok := paths[path]; ok {
			t.Fatalf("public gateway spec must not include non-gateway route %s", path)
		}
	}
}

func assertGatewaySecuritySchemes(t *testing.T, document map[string]any) {
	t.Helper()
	components := asMap(t, document["components"], "components")
	schemes := asMap(t, components["securitySchemes"], "components.securitySchemes")
	for _, name := range []string{"TokenHubProjectKey", "TokenHubXAPIKey", "TokenHubGoogleAPIKey"} {
		if _, ok := schemes[name]; !ok {
			t.Fatalf("missing %s security scheme", name)
		}
	}
	security, ok := document["security"].([]any)
	if !ok {
		t.Fatal("missing global security requirements")
	}
	for _, name := range []string{"TokenHubProjectKey", "TokenHubXAPIKey", "TokenHubGoogleAPIKey"} {
		found := false
		for _, rawRequirement := range security {
			requirement := asMap(t, rawRequirement, "security requirement")
			if _, ok := requirement[name]; ok {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("global security requirements missing %s option", name)
		}
	}
	responses := asMap(t, components["responses"], "components.responses")
	for name, rawResponse := range responses {
		response := asMap(t, rawResponse, "components.responses."+name)
		headers := asMap(t, response["headers"], "components.responses."+name+".headers")
		if _, ok := headers["x-request-id"]; !ok {
			t.Fatalf("%s response missing x-request-id header", name)
		}
	}
	for _, name := range []string{"QuotaExceeded", "AnthropicQuotaExceeded"} {
		response := asMap(t, responses[name], "components.responses."+name)
		headers := asMap(t, response["headers"], "components.responses."+name+".headers")
		for _, header := range append(commonRateLimitHeaders(), "retry-after") {
			if _, ok := headers[header]; !ok {
				t.Fatalf("%s response missing %s header", name, header)
			}
		}
	}
	requireSchemaProperty(t, document, "ErrorResponse", "request_id")
	requireSchemaProperty(t, document, "AnthropicErrorResponse", "request_id")
	modelNotAllowed := asMap(t, responses["ModelNotAllowed"], "components.responses.ModelNotAllowed")
	content := asMap(t, modelNotAllowed["content"], "ModelNotAllowed.content")
	jsonContent := asMap(t, content["application/json"], "ModelNotAllowed application/json")
	examples := asMap(t, jsonContent["examples"], "ModelNotAllowed examples")
	example := asMap(t, examples["modelNotAllowed"], "ModelNotAllowed example")
	value := asMap(t, example["value"], "ModelNotAllowed example value")
	errorObject := asMap(t, value["error"], "ModelNotAllowed example error")
	if got := errorObject["code"]; got != "model_not_allowed" {
		t.Fatalf("ModelNotAllowed example code=%v, want model_not_allowed", got)
	}
	assertErrorExampleCodeAndTypeMatch(t, responses, "ModelNotAllowed", "modelNotAllowed")
	assertErrorExampleCodeAndTypeMatch(t, responses, "ImageMaskNotSupported", "codexMaskUnsupported")
}

func assertErrorExampleCodeAndTypeMatch(t *testing.T, responses map[string]any, responseName string, exampleName string) {
	t.Helper()
	response := asMap(t, responses[responseName], "components.responses."+responseName)
	content := asMap(t, response["content"], responseName+".content")
	jsonContent := asMap(t, content["application/json"], responseName+" application/json")
	examples := asMap(t, jsonContent["examples"], responseName+" examples")
	example := asMap(t, examples[exampleName], responseName+" example "+exampleName)
	value := asMap(t, example["value"], responseName+" example "+exampleName+" value")
	errorObject := asMap(t, value["error"], responseName+" example "+exampleName+" error")
	code, _ := errorObject["code"].(string)
	if code == "" {
		t.Fatalf("%s example %s missing error.code", responseName, exampleName)
	}
	if got := errorObject["type"]; got != code {
		t.Fatalf("%s example %s error.type=%v, want %s", responseName, exampleName, got, code)
	}
}

func assertCommonGatewayHeadersAndErrors(t *testing.T, document map[string]any) {
	t.Helper()
	for operation := range documentedGatewayOperations(document) {
		rawOperation := openAPIOperation(t, document, operation.Path, operation.Method)
		responses := asMap(t, rawOperation["responses"], operation.Method+" "+operation.Path+" responses")
		success := resolveOpenAPIRef(t, document, responses["200"], operation.Method+" "+operation.Path+" response 200")
		if !signedImageAssetOperation(operation) {
			headers, hasHeaders := success["headers"].(map[string]any)
			if successResponseEmitsRequestID(operation) {
				if _, ok := headers["x-request-id"]; !ok {
					t.Fatalf("%s %s response 200 missing x-request-id header", operation.Method, operation.Path)
				}
			} else if hasHeaders {
				if _, ok := headers["x-request-id"]; ok {
					t.Fatalf("%s %s response 200 documents x-request-id, but runtime does not emit it", operation.Method, operation.Path)
				}
			}
			if rateLimitedSuccessOperation(operation) {
				for _, header := range commonRateLimitHeaders() {
					if _, ok := headers[header]; !ok {
						t.Fatalf("%s %s response 200 missing rate-limit header %s", operation.Method, operation.Path, header)
					}
				}
			} else if hasHeaders {
				for _, header := range commonRateLimitHeaders() {
					if _, ok := headers[header]; ok {
						t.Fatalf("%s %s response 200 documents rate-limit header %s, but runtime does not emit it", operation.Method, operation.Path, header)
					}
				}
			}
		}
		if !signedImageAssetOperation(operation) {
			if _, ok := responses["401"]; !ok {
				t.Fatalf("%s %s missing common response 401", operation.Method, operation.Path)
			}
			if rateLimitedSuccessOperation(operation) {
				if _, ok := responses["429"]; !ok {
					t.Fatalf("%s %s missing quota response 429", operation.Method, operation.Path)
				}
			} else if _, ok := responses["429"]; ok {
				t.Fatalf("%s %s documents quota response 429, but runtime does not admit or rate-limit the call", operation.Method, operation.Path)
			}
		}
		if providerRoutedOperation(operation) {
			if _, ok := responses["502"]; !ok {
				t.Fatalf("%s %s missing provider error response", operation.Method, operation.Path)
			}
		} else if _, ok := responses["502"]; ok {
			t.Fatalf("%s %s documents provider error response, but runtime handles the operation locally", operation.Method, operation.Path)
		}
		if modelAccessControlledOperation(operation) {
			if _, ok := responses["403"]; !ok {
				t.Fatalf("%s %s missing model-access error response", operation.Method, operation.Path)
			}
		}
		if providerCapabilityOperation(operation) {
			if _, ok := responses["501"]; !ok {
				t.Fatalf("%s %s missing provider capability response", operation.Method, operation.Path)
			}
		}
	}
}

func commonRateLimitHeaders() []string {
	return []string{
		"x-ratelimit-limit-requests",
		"x-ratelimit-remaining-requests",
		"x-ratelimit-reset-requests",
		"x-ratelimit-limit-tokens",
		"x-ratelimit-remaining-tokens",
		"x-ratelimit-reset-tokens",
	}
}

func signedImageAssetOperation(operation gatewayOperation) bool {
	return operation.Method == "GET" && operation.Path == "/v1/image-assets/{asset_id}/content"
}

func successResponseEmitsRequestID(operation gatewayOperation) bool {
	return rateLimitedSuccessOperation(operation) ||
		operation.Path == "/v1/responses/{response_id}" ||
		operation.Path == "/v1/responses/{response_id}/cancel"
}

func rateLimitedSuccessOperation(operation gatewayOperation) bool {
	return providerRoutedOperation(operation)
}

func providerRoutedOperation(operation gatewayOperation) bool {
	switch operation.Path {
	case "/v1/chat/completions",
		"/v1/responses",
		"/v1/responses/compact",
		"/v1/messages",
		"/v1/embeddings",
		"/v1/images/generations",
		"/v1/images/edits",
		"/v1beta/models/{model}:generateContent",
		"/v1beta/models/{model}:streamGenerateContent":
		return true
	default:
		return false
	}
}

func providerCapabilityOperation(operation gatewayOperation) bool {
	switch operation.Path {
	case "/v1/responses",
		"/v1/responses/compact",
		"/v1/messages",
		"/v1beta/models/{model}:generateContent",
		"/v1beta/models/{model}:streamGenerateContent":
		return true
	default:
		return false
	}
}

func modelAccessControlledOperation(operation gatewayOperation) bool {
	switch operation.Path {
	case "/v1/chat/completions",
		"/v1/responses",
		"/v1/responses/compact",
		"/v1/messages",
		"/v1/messages/count_tokens",
		"/v1/embeddings",
		"/v1/images/generations",
		"/v1/images/edits",
		"/v1beta/models/{model}:generateContent",
		"/v1beta/models/{model}:streamGenerateContent",
		"/v1beta/models/{model}:countTokens":
		return true
	default:
		return false
	}
}

func documentedGatewayOperations(document map[string]any) map[gatewayOperation]bool {
	operations := make(map[gatewayOperation]bool)
	paths, _ := document["paths"].(map[string]any)
	for path, rawItem := range paths {
		item, _ := rawItem.(map[string]any)
		for method, rawOperation := range item {
			switch method {
			case "get", "post", "put", "patch", "delete":
			default:
				continue
			}
			operation, _ := rawOperation.(map[string]any)
			if operation["x-tokenhub-public-gateway"] == true {
				operations[gatewayOperation{Method: strings.ToUpper(method), Path: path}] = true
			}
		}
	}
	return operations
}

func registeredGatewayOperations(t *testing.T) map[gatewayOperation]bool {
	t.Helper()
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "test_admin_token"})
	operations := make(map[gatewayOperation]bool, len(server.publicGatewayOperations))
	for operation := range server.publicGatewayOperations {
		operations[operation] = true
	}
	return operations
}

func operationDiff(left, right map[gatewayOperation]bool) []string {
	var diff []string
	for operation := range left {
		if !right[operation] {
			diff = append(diff, operation.Method+" "+operation.Path)
		}
	}
	sort.Strings(diff)
	return diff
}

func assertUniqueOperationIDs(t *testing.T, document map[string]any) {
	t.Helper()
	seen := make(map[string]string)
	paths, _ := document["paths"].(map[string]any)
	for path, rawItem := range paths {
		item, _ := rawItem.(map[string]any)
		for method, rawOperation := range item {
			operation, _ := rawOperation.(map[string]any)
			id, _ := operation["operationId"].(string)
			if id == "" {
				t.Fatalf("%s %s has no operationId", strings.ToUpper(method), path)
			}
			if previous := seen[id]; previous != "" {
				t.Fatalf("duplicate operationId %q on %s %s and %s", id, strings.ToUpper(method), path, previous)
			}
			seen[id] = strings.ToUpper(method) + " " + path
		}
	}
}

func assertLocalRefsResolve(t *testing.T, document map[string]any) {
	t.Helper()
	var visit func(any)
	visit = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			if ref, _ := typed["$ref"].(string); ref != "" {
				if !strings.HasPrefix(ref, "#/") || !localRefExists(document, strings.TrimPrefix(ref, "#/")) {
					t.Fatalf("unresolved OpenAPI ref %q", ref)
				}
			}
			for _, nested := range typed {
				visit(nested)
			}
		case []any:
			for _, nested := range typed {
				visit(nested)
			}
		}
	}
	visit(document)
}

func localRefExists(document map[string]any, pointer string) bool {
	var current any = document
	for _, segment := range strings.Split(pointer, "/") {
		segment = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			return false
		}
		current, ok = object[segment]
		if !ok {
			return false
		}
	}
	return true
}

func requireExampleValueContains(t *testing.T, document map[string]any, path string, method string, status string, mediaType string, exampleName string, want string) {
	t.Helper()
	operation := openAPIOperation(t, document, path, method)
	responses := asMap(t, operation["responses"], strings.ToUpper(method)+" "+path+" responses")
	response := resolveOpenAPIRef(t, document, responses[status], strings.ToUpper(method)+" "+path+" response "+status)
	content := asMap(t, response["content"], strings.ToUpper(method)+" "+path+" response "+status+" content")
	media := asMap(t, content[mediaType], strings.ToUpper(method)+" "+path+" "+mediaType)
	examples := asMap(t, media["examples"], strings.ToUpper(method)+" "+path+" examples")
	example := resolveOpenAPIRef(t, document, examples[exampleName], strings.ToUpper(method)+" "+path+" example "+exampleName)
	value, _ := example["value"].(string)
	if !strings.Contains(value, want) {
		t.Fatalf("%s %s example %q value must contain %q, got %q", strings.ToUpper(method), path, exampleName, want, value)
	}
}

func requireRequestExampleField(t *testing.T, document map[string]any, path string, method string, exampleName string, field string, want any) {
	t.Helper()
	requireRequestExampleFieldForMedia(t, document, path, method, "application/json", exampleName, field, want)
}

func requireRequestExampleFieldForMedia(t *testing.T, document map[string]any, path string, method string, mediaType string, exampleName string, field string, want any) {
	t.Helper()
	operation := openAPIOperation(t, document, path, method)
	content := operationRequestContent(t, operation, strings.ToUpper(method)+" "+path)
	media := asMap(t, content[mediaType], strings.ToUpper(method)+" "+path+" "+mediaType)
	examples := asMap(t, media["examples"], strings.ToUpper(method)+" "+path+" request examples")
	example := resolveOpenAPIRef(t, document, examples[exampleName], strings.ToUpper(method)+" "+path+" example "+exampleName)
	value := asMap(t, example["value"], strings.ToUpper(method)+" "+path+" example "+exampleName+" value")
	if got := value[field]; got != want {
		t.Fatalf("%s %s example %q field %q=%v, want %v", strings.ToUpper(method), path, exampleName, field, got, want)
	}
}

func requireResponseExampleValueContains(t *testing.T, document map[string]any, path string, method string, status string, mediaType string, exampleName string, want string) {
	t.Helper()
	operation := openAPIOperation(t, document, path, method)
	responses := asMap(t, operation["responses"], strings.ToUpper(method)+" "+path+" responses")
	response := resolveOpenAPIRef(t, document, responses[status], strings.ToUpper(method)+" "+path+" response "+status)
	content := asMap(t, response["content"], strings.ToUpper(method)+" "+path+" response "+status+" content")
	media := asMap(t, content[mediaType], strings.ToUpper(method)+" "+path+" "+mediaType)
	examples := asMap(t, media["examples"], strings.ToUpper(method)+" "+path+" response examples")
	example := resolveOpenAPIRef(t, document, examples[exampleName], strings.ToUpper(method)+" "+path+" example "+exampleName)
	value, _ := example["value"].(string)
	if !strings.Contains(value, want) {
		t.Fatalf("%s %s response example %q must contain %q, got %q", strings.ToUpper(method), path, exampleName, want, value)
	}
}

func requireResponseRef(t *testing.T, document map[string]any, path string, method string, status string, want string) {
	t.Helper()
	operation := openAPIOperation(t, document, path, method)
	responses := asMap(t, operation["responses"], strings.ToUpper(method)+" "+path+" responses")
	response := asMap(t, responses[status], strings.ToUpper(method)+" "+path+" response "+status)
	if got := response["$ref"]; got != want {
		t.Fatalf("%s %s response %s ref=%v, want %s", strings.ToUpper(method), path, status, got, want)
	}
	if !localRefExists(document, strings.TrimPrefix(want, "#/")) {
		t.Fatalf("%s %s response %s ref target does not exist: %s", strings.ToUpper(method), path, status, want)
	}
}

func requireRequestContentType(t *testing.T, document map[string]any, path string, method string, mediaType string) {
	t.Helper()
	operation := openAPIOperation(t, document, path, method)
	content := operationRequestContent(t, operation, strings.ToUpper(method)+" "+path)
	if _, ok := content[mediaType]; !ok {
		t.Fatalf("%s %s request must document %s", strings.ToUpper(method), path, mediaType)
	}
}

func requireRequestSchemaRef(t *testing.T, document map[string]any, path string, method string, want string) {
	t.Helper()
	operation := openAPIOperation(t, document, path, method)
	content := operationRequestContent(t, operation, strings.ToUpper(method)+" "+path)
	media := asMap(t, content["application/json"], strings.ToUpper(method)+" "+path+" application/json")
	schema := asMap(t, media["schema"], strings.ToUpper(method)+" "+path+" request schema")
	if got := schema["$ref"]; got != want {
		t.Fatalf("%s %s request schema ref=%v, want %s", strings.ToUpper(method), path, got, want)
	}
}

func requireResponseContentType(t *testing.T, document map[string]any, path string, method string, status string, mediaType string) {
	t.Helper()
	operation := openAPIOperation(t, document, path, method)
	responses := asMap(t, operation["responses"], strings.ToUpper(method)+" "+path+" responses")
	response := resolveOpenAPIRef(t, document, responses[status], strings.ToUpper(method)+" "+path+" response "+status)
	content := asMap(t, response["content"], strings.ToUpper(method)+" "+path+" response "+status+" content")
	if _, ok := content[mediaType]; !ok {
		t.Fatalf("%s %s response %s must document %s", strings.ToUpper(method), path, status, mediaType)
	}
}

func requireOperationParameter(t *testing.T, document map[string]any, path string, method string, in string, name string) {
	t.Helper()
	operation := openAPIOperation(t, document, path, method)
	parameters, ok := operation["parameters"].([]any)
	if !ok {
		t.Fatalf("%s %s has no parameters", strings.ToUpper(method), path)
	}
	for _, rawParameter := range parameters {
		parameter := resolveOpenAPIRef(t, document, rawParameter, strings.ToUpper(method)+" "+path+" parameter")
		if parameter["in"] == in && parameter["name"] == name {
			return
		}
	}
	t.Fatalf("%s %s must document %s parameter %s", strings.ToUpper(method), path, in, name)
}

func requireSchemaNoProperty(t *testing.T, document map[string]any, schemaName string, property string) {
	t.Helper()
	schema := asMap(t, localRefValue(document, "components/schemas/"+schemaName), "components.schemas."+schemaName)
	properties := asMap(t, schema["properties"], "components.schemas."+schemaName+".properties")
	if _, ok := properties[property]; ok {
		t.Fatalf("schema %s must not document ignored property %s", schemaName, property)
	}
}

func requireSchemaProperty(t *testing.T, document map[string]any, schemaName string, property string) {
	t.Helper()
	schema := asMap(t, localRefValue(document, "components/schemas/"+schemaName), "components.schemas."+schemaName)
	properties := asMap(t, schema["properties"], "components.schemas."+schemaName+".properties")
	if _, ok := properties[property]; !ok {
		t.Fatalf("schema %s must document property %s", schemaName, property)
	}
}

func requireSchemaPropertyNoConst(t *testing.T, document map[string]any, schemaName string, property string) {
	t.Helper()
	schema := asMap(t, localRefValue(document, "components/schemas/"+schemaName), "components.schemas."+schemaName)
	properties := asMap(t, schema["properties"], "components.schemas."+schemaName+".properties")
	propertySchema := asMap(t, properties[property], "components.schemas."+schemaName+".properties."+property)
	if value, ok := propertySchema["const"]; ok {
		t.Fatalf("schema %s property %s must not use const, got %v", schemaName, property, value)
	}
}

func requireSchemaDescriptionContains(t *testing.T, document map[string]any, schemaName string, want string) {
	t.Helper()
	schema := asMap(t, localRefValue(document, "components/schemas/"+schemaName), "components.schemas."+schemaName)
	if description, _ := schema["description"].(string); !strings.Contains(description, want) {
		t.Fatalf("schema %s description must contain %q, got %q", schemaName, want, description)
	}
}

func requireArrayItemsRef(t *testing.T, document map[string]any, schemaName string, property string, want string) {
	t.Helper()
	schema := asMap(t, localRefValue(document, "components/schemas/"+schemaName), "components.schemas."+schemaName)
	properties := asMap(t, schema["properties"], "components.schemas."+schemaName+".properties")
	propertySchema := asMap(t, properties[property], "components.schemas."+schemaName+".properties."+property)
	if got := asMap(t, propertySchema["items"], "items")["$ref"]; got != want {
		t.Fatalf("schema %s property %s items ref=%v, want %s", schemaName, property, got, want)
	}
}

func requireOneOfContainsRef(t *testing.T, document map[string]any, schemaName string, property string, want string) {
	t.Helper()
	schema := asMap(t, localRefValue(document, "components/schemas/"+schemaName), "components.schemas."+schemaName)
	properties := asMap(t, schema["properties"], "components.schemas."+schemaName+".properties")
	propertySchema := asMap(t, properties[property], "components.schemas."+schemaName+".properties."+property)
	for _, rawOption := range asSlice(t, propertySchema["oneOf"], "oneOf") {
		option := asMap(t, rawOption, "oneOf option")
		if got := option["$ref"]; got == want {
			return
		}
		if option["type"] != "array" {
			continue
		}
		items := asMap(t, option["items"], "array option items")
		if got := items["$ref"]; got == want {
			return
		}
	}
	t.Fatalf("schema %s property %s oneOf must contain ref %s", schemaName, property, want)
}

func requireSchemaRequired(t *testing.T, document map[string]any, schemaName string, want []string) {
	t.Helper()
	schema := asMap(t, localRefValue(document, "components/schemas/"+schemaName), "components.schemas."+schemaName)
	rawRequired, ok := schema["required"].([]any)
	if !ok {
		t.Fatalf("schema %s must declare required fields", schemaName)
	}
	got := make([]string, 0, len(rawRequired))
	for _, raw := range rawRequired {
		if field, ok := raw.(string); ok {
			got = append(got, field)
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("schema %s required=%v, want %v", schemaName, got, want)
	}
}

func requireSchemaEnum(t *testing.T, document map[string]any, schemaName string, property string, want []string) {
	t.Helper()
	schema := asMap(t, localRefValue(document, "components/schemas/"+schemaName), "components.schemas."+schemaName)
	properties := asMap(t, schema["properties"], "components.schemas."+schemaName+".properties")
	propertySchema := asMap(t, properties[property], "components.schemas."+schemaName+".properties."+property)
	rawEnum, ok := propertySchema["enum"].([]any)
	if !ok {
		t.Fatalf("schema %s property %s must declare an enum", schemaName, property)
	}
	var got []string
	for _, raw := range rawEnum {
		value, _ := raw.(string)
		got = append(got, value)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("schema %s property %s enum=%v, want %v", schemaName, property, got, want)
	}
}

func requireOperationSecurityEmpty(t *testing.T, document map[string]any, path string, method string) {
	t.Helper()
	operation := openAPIOperation(t, document, path, method)
	security, ok := operation["security"].([]any)
	if !ok || len(security) != 0 {
		t.Fatalf("%s %s must override global security with an empty security array", strings.ToUpper(method), path)
	}
}

func requireNoResponseContentType(t *testing.T, document map[string]any, path string, method string, status string, mediaType string) {
	t.Helper()
	operation := openAPIOperation(t, document, path, method)
	responses := asMap(t, operation["responses"], strings.ToUpper(method)+" "+path+" responses")
	response := resolveOpenAPIRef(t, document, responses[status], strings.ToUpper(method)+" "+path+" response "+status)
	content := asMap(t, response["content"], strings.ToUpper(method)+" "+path+" response "+status+" content")
	if _, ok := content[mediaType]; ok {
		t.Fatalf("%s %s response %s must not document %s", strings.ToUpper(method), path, status, mediaType)
	}
}

func requireResponseHeader(t *testing.T, document map[string]any, path string, method string, status string, header string) {
	t.Helper()
	operation := openAPIOperation(t, document, path, method)
	responses := asMap(t, operation["responses"], strings.ToUpper(method)+" "+path+" responses")
	response := resolveOpenAPIRef(t, document, responses[status], strings.ToUpper(method)+" "+path+" response "+status)
	headers := asMap(t, response["headers"], strings.ToUpper(method)+" "+path+" response "+status+" headers")
	if _, ok := headers[header]; !ok {
		t.Fatalf("%s %s response %s must document %s header", strings.ToUpper(method), path, status, header)
	}
}

func requireResponseRateLimitHeaders(t *testing.T, document map[string]any, path string, method string, status string) {
	t.Helper()
	for _, header := range commonRateLimitHeaders() {
		requireResponseHeader(t, document, path, method, status, header)
	}
}

func requireNoResponseHeader(t *testing.T, document map[string]any, path string, method string, status string, header string) {
	t.Helper()
	operation := openAPIOperation(t, document, path, method)
	responses := asMap(t, operation["responses"], strings.ToUpper(method)+" "+path+" responses")
	response := resolveOpenAPIRef(t, document, responses[status], strings.ToUpper(method)+" "+path+" response "+status)
	headers, _ := response["headers"].(map[string]any)
	if _, ok := headers[header]; ok {
		t.Fatalf("%s %s response %s must not document %s header", strings.ToUpper(method), path, status, header)
	}
}

func requireInteractiveFlag(t *testing.T, document map[string]any, path string, method string, want bool) {
	t.Helper()
	operation := openAPIOperation(t, document, path, method)
	if got := operation["x-tokenhub-interactive"]; got != want {
		t.Fatalf("%s %s x-tokenhub-interactive=%v, want %v", strings.ToUpper(method), path, got, want)
	}
}

func openAPIOperation(t *testing.T, document map[string]any, path string, method string) map[string]any {
	t.Helper()
	paths := asMap(t, document["paths"], "paths")
	item := asMap(t, paths[path], "paths."+path)
	return asMap(t, item[strings.ToLower(method)], strings.ToUpper(method)+" "+path)
}

func operationRequestContent(t *testing.T, operation map[string]any, label string) map[string]any {
	t.Helper()
	requestBody := asMap(t, operation["requestBody"], label+" requestBody")
	return asMap(t, requestBody["content"], label+" requestBody content")
}

func resolveOpenAPIRef(t *testing.T, document map[string]any, value any, label string) map[string]any {
	t.Helper()
	object := asMap(t, value, label)
	ref, _ := object["$ref"].(string)
	if ref == "" {
		return object
	}
	if !strings.HasPrefix(ref, "#/") {
		t.Fatalf("%s has non-local ref %q", label, ref)
	}
	resolved, ok := localRefValue(document, strings.TrimPrefix(ref, "#/")).(map[string]any)
	if !ok {
		t.Fatalf("%s has unresolved ref %q", label, ref)
	}
	return resolved
}

func localRefValue(document map[string]any, pointer string) any {
	var current any = document
	for _, segment := range strings.Split(pointer, "/") {
		segment = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current, ok = object[segment]
		if !ok {
			return nil
		}
	}
	return current
}

func asMap(t *testing.T, value any, label string) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s is not an object", label)
	}
	return object
}

func asSlice(t *testing.T, value any, label string) []any {
	t.Helper()
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("%s is not an array", label)
	}
	return items
}

func jsonRoundTrip(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json round trip: %v", err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal json round trip: %v", err)
	}
	return out
}

func rewriteSchemaRefs(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			if key == "$ref" {
				if ref, ok := nested.(string); ok && strings.HasPrefix(ref, "#/components/schemas/") {
					out[key] = "#/$defs/" + strings.TrimPrefix(ref, "#/components/schemas/")
					continue
				}
			}
			out[key] = rewriteSchemaRefs(nested)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, nested := range typed {
			out[i] = rewriteSchemaRefs(nested)
		}
		return out
	default:
		return value
	}
}

func sanitizeSchemaResourceName(label string) string {
	sanitized := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, label)
	return strings.Trim(sanitized, "-")
}
