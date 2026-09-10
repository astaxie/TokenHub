#!/bin/sh
payload="$(cat)"

require_fragment() {
  fragment="$1"
  message="$2"
  case "$payload" in
    *"$fragment"*) ;;
    *)
      printf '%s\n' "$message" >&2
      exit 2
      ;;
  esac
}

case "$payload" in
  *'"api_key":"provider-secret"'*) ;;
  *)
    printf 'missing provider credentials\n' >&2
    exit 2
    ;;
esac

case "$payload" in
  *'"provider":{"id":"prv_external_mock"'*'"type":"external_mock"'*) ;;
  *)
    printf 'missing provider projection\n' >&2
    exit 2
    ;;
esac

case "$payload" in
  *'"operation":"chat"'*'"provider_model":"external-upstream-chat"'*)
    require_fragment '"request":' 'missing chat request body'
    require_fragment '"model":' 'missing chat request model'
    require_fragment '"messages":' 'missing chat request messages'
    require_fragment 'hello' 'missing chat request content'
    cat <<'JSON'
{"response":{"id":"chatcmpl_external_mock","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"external mock chat"}}]},"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}
JSON
    ;;
  *'"operation":"chat_stream"'*'"provider_model":"external-upstream-chat-stream"'*'"stream":true'*)
    require_fragment '"request":' 'missing chat stream request body'
    require_fragment '"model":' 'missing chat stream request model'
    require_fragment '"messages":' 'missing chat stream request messages'
    require_fragment 'hello' 'missing chat stream request content'
    cat <<'JSON'
{"events":[{"data":{"id":"chatcmpl_external_mock_stream","object":"chat.completion.chunk","choices":[{"delta":{"content":"external mock stream"}}]}},{"data":"[DONE]"}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}
JSON
    ;;
  *'"operation":"responses"'*'"provider_model":"external-upstream-responses"'*)
    require_fragment '"request":' 'missing responses request body'
    require_fragment '"model":' 'missing responses request model'
    require_fragment '"input":"hello"' 'missing responses request input'
    cat <<'JSON'
{"response":{"id":"resp_external_mock","object":"response","status":"completed","output_text":"external mock responses"},"usage":{"prompt_tokens":4,"completion_tokens":5,"total_tokens":9}}
JSON
    ;;
  *'"operation":"responses_stream"'*'"provider_model":"external-upstream-responses-stream"'*'"stream":true'*)
    require_fragment '"request":' 'missing responses stream request body'
    require_fragment '"model":' 'missing responses stream request model'
    require_fragment '"input":"hello"' 'missing responses stream request input'
    cat <<'JSON'
{"events":[{"event":"response.output_text.delta","data":{"type":"response.output_text.delta","delta":"external mock responses stream"}},{"event":"response.completed","data":{"type":"response.completed","response":{"id":"resp_external_mock_stream","status":"completed","output":[],"usage":{"input_tokens":5,"output_tokens":6,"total_tokens":11}}}}]}
JSON
    ;;
  *'"operation":"embeddings"'*'"provider_model":"external-upstream-embeddings"'*)
    require_fragment '"request":' 'missing embeddings request body'
    require_fragment '"model":' 'missing embeddings request model'
    require_fragment '"input":"hello"' 'missing embeddings request input'
    cat <<'JSON'
{"response":{"object":"list","data":[{"index":0,"embedding":[0.1,0.2,0.3]}],"model":"external-upstream-embeddings"},"usage":{"prompt_tokens":6,"completion_tokens":0,"total_tokens":6}}
JSON
    ;;
  *'"operation":"models"'*)
    cat <<'JSON'
{"status":200,"catalog":{"id":"external-mock","name":"External Mock","display_name":"External Mock","type":"external_mock","models_count":2,"source":"plugin-live","etag":"mock-etag","models":[{"id":"external-mock-chat","name":"external-mock-chat","type":"chat"},{"id":"external-mock-embed","name":"external-mock-embed","type":"embedding"}]}}
JSON
    ;;
  *'"operation":"probe"'*)
    cat <<'JSON'
{"result":{"resource_id":"rsrc_external_mock","model":"external-upstream-chat","output_text":"external mock provider is reachable","latency_ms":12}}
JSON
    ;;
  *)
    printf 'unsupported provider operation\n' >&2
    exit 2
    ;;
esac
