#!/bin/sh
payload="$(cat)"

contains() {
	case "$payload" in
		*"$1"*) printf true ;;
		*) printf false ;;
	esac
}

if [ "$(contains 'req_external_guardrail_fail_command')" = true ]; then
	failure_requested=true
fi

saw_auth_context=$(contains '"auth_context"')
saw_project_metadata=$(contains '"project_metadata"')
saw_api_key_metadata=$(contains '"api_key_metadata"')
saw_provider_response=$(contains '"provider_response"')
saw_stream_events=$(contains '"stream_events"')
saw_usage=$(contains '"usage"')
unsafe_output=$(contains 'unsafe prompt sentinel')
if [ "$failure_requested" = true ]; then
	unsafe_output=true
fi
leaked_secret=$(contains 'provider-secret')

if [ "$unsafe_output" = true ]; then
	cat <<JSON
{"decision":"deny","audit_events":[{"event":"external_guardrail_hook","saw_auth_context":$saw_auth_context,"saw_project_metadata":$saw_project_metadata,"saw_api_key_metadata":$saw_api_key_metadata,"saw_provider_response":$saw_provider_response,"saw_stream_events":$saw_stream_events,"saw_usage":$saw_usage,"unsafe_output":true,"leaked_secret":$leaked_secret}]}
JSON
	exit 0
fi

cat <<JSON
{"decision":"continue","writes":{"provider_response":{"value":{"id":"resp_external_guardrail","object":"response","status":"completed","output_text":"guardrail approved"}}},"audit_events":[{"event":"external_guardrail_hook","saw_auth_context":$saw_auth_context,"saw_project_metadata":$saw_project_metadata,"saw_api_key_metadata":$saw_api_key_metadata,"saw_provider_response":$saw_provider_response,"saw_stream_events":$saw_stream_events,"saw_usage":$saw_usage,"unsafe_output":false,"leaked_secret":$leaked_secret}]}
JSON
