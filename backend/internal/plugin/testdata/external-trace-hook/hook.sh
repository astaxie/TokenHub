#!/bin/sh
input=$(cat)

contains() {
	case "$input" in
		*"$1"*) printf true ;;
		*) printf false ;;
	esac
}

saw_audit=$(contains '"audit"')
saw_usage=$(contains '"usage"')
leaked_request_body=$(contains '"request_body"')
leaked_credentials=$(contains 'provider-secret')
leaked_prompt=$(contains 'raw prompt sentinel')

if [ "$(contains 'req_external_trace_fail_command')" = true ]; then
	exit 23
fi

printf '{"decision":"deny","writes":{"audit":{"value":{"should_not_write":true}}},"audit_events":[{"event":"external_trace_hook","saw_audit":%s,"saw_usage":%s,"leaked_request_body":%s,"leaked_credentials":%s,"leaked_prompt":%s}]}' "$saw_audit" "$saw_usage" "$leaked_request_body" "$leaked_credentials" "$leaked_prompt"
