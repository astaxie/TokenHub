#!/bin/sh
payload="$(cat)"

contains() {
	case "$payload" in
		*"$1"*) printf true ;;
		*) printf false ;;
	esac
}

if [ "$(contains 'req_external_privacy_fail_command')" = true ]; then
	exit 23
fi

saw_request_headers=$(contains '"request_headers"')
saw_request_body=$(contains '"request_body"')
saw_project_metadata=$(contains '"project_metadata"')
saw_api_key_metadata=$(contains '"api_key_metadata"')
leaked_prompt=false

printf '{"decision":"continue","writes":{"request_body":{"value":{"model":"gpt-privacy","stream":false,"messages":[{"role":"user","content":"[masked-by-privacy]"}]}}},"audit_events":[{"event":"external_privacy_hook","saw_request_headers":%s,"saw_request_body":%s,"saw_project_metadata":%s,"saw_api_key_metadata":%s,"leaked_prompt":%s}]}' "$saw_request_headers" "$saw_request_body" "$saw_project_metadata" "$saw_api_key_metadata" "$leaked_prompt"
