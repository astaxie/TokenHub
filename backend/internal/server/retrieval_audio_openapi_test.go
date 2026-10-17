package server

import "testing"

func TestRerankDocumentIdentityOpenAPIContract(t *testing.T) {
	document := mediaOpenAPIDocument(t)
	for _, request := range []string{
		`{"model":"reranker","query":"query","documents":["first","second"]}`,
		`{"model":"reranker","query":"query","documents":[{"id":"doc-1","text":"first"}],"top_n":1,"return_documents":true}`,
		`{"model":"reranker","query":"query","documents":["first",{"id":"doc-2","text":"second"}]}`,
	} {
		assertMediaOperationSchema(t, document, "/v1/rerank", "post", "", request)
	}
	for _, response := range []string{
		`{"model":"reranker","results":[{"index":1,"id":"doc-2","relevance_score":0.9}]}`,
		`{"model":"reranker","results":[{"index":0,"id":"doc-1","relevance_score":0.9,"document":{"text":"first"}}]}`,
	} {
		assertMediaOperationSchema(t, document, "/v1/rerank", "post", "200", response)
	}
}

func TestTranscriptionOpenAPIRequiresMultipartFile(t *testing.T) {
	document := mediaOpenAPIDocument(t)
	for _, path := range []string{"/v1/audio/transcriptions", "/v1/audio/translations"} {
		operation := openAPIOperation(t, document, path, "post")
		content := operationRequestContent(t, operation, path)
		upload := asMap(t, content["multipart/form-data"], path)
		schema := asMap(t, upload["schema"], path)
		if schema["$ref"] != "#/components/schemas/AudioTranscriptionUploadRequest" {
			t.Fatalf("%s does not declare the transcription upload contract", path)
		}
		assertExampleMatchesSchema(t, document, schema, map[string]any{"model": "transcriber", "file": "synthetic binary"}, path)
	}
	components := asMap(t, document["components"], "components")
	schemas := asMap(t, components["schemas"], "schemas")
	upload := asMap(t, schemas["AudioTranscriptionUploadRequest"], "upload schema")
	required, ok := upload["required"].([]any)
	if !ok || len(required) != 2 || required[0] != "model" || required[1] != "file" {
		t.Fatal("transcription multipart must require both model and file")
	}
}
