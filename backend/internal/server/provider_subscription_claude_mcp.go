package server

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
)

func (r *claudeSubscriptionRun) handleMCP(w http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/mcp" || subtle.ConstantTimeCompare([]byte(request.Header.Get("Authorization")), []byte("Bearer "+r.token)) != 1 {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if request.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var rpc struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if json.NewDecoder(io.LimitReader(request.Body, 1<<20)).Decode(&rpc) != nil || rpc.JSONRPC != "2.0" {
		http.Error(w, "Invalid MCP request", 400)
		return
	}
	if len(rpc.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var result any
	rpcError := func(message string) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "error": map[string]any{"code": -32602, "message": message}})
	}
	switch rpc.Method {
	case "initialize":
		result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "tokenhub", "version": "1"}}
	case "ping":
		result = map[string]any{}
	case "tools/list":
		result = map[string]any{"tools": r.tools}
	case "tools/call":
		var input struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
			Meta      map[string]any `json:"_meta"`
		}
		if json.Unmarshal(rpc.Params, &input) != nil {
			rpcError("Invalid tool request")
			return
		}
		id, _ := input.Meta["claudecode/toolUseId"].(string)
		if id == "" || len(id) > 256 {
			rpcError("Claude tool request id is required")
			return
		}
		r.mu.Lock()
		pending := r.internal[id]
		if pending == nil && len(r.internal) < 128 {
			pending = &claudeSubscriptionPending{internalID: id, ready: make(chan struct{}), result: make(chan string, 1)}
			r.internal[id] = pending
		}
		r.mu.Unlock()
		if pending == nil {
			rpcError("Too many tool requests")
			return
		}
		select {
		case <-pending.ready:
		case <-request.Context().Done():
			return
		case <-r.ctx.Done():
			return
		}
		r.mu.Lock()
		valid := !pending.claimed && pending.tool.Name == input.Name && reflect.DeepEqual(pending.arguments, input.Arguments)
		if valid {
			pending.claimed = true
		}
		r.mu.Unlock()
		if !valid {
			rpcError("Tool request does not match the generated call")
			return
		}
		select {
		case text := <-pending.result:
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": false}
		case <-request.Context().Done():
			r.close()
			return
		case <-r.ctx.Done():
			return
		}
	default:
		rpcError("Unsupported MCP method")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result})
}

func (r *claudeSubscriptionRun) registerCalls(blocks []any) ([]map[string]any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var calls []map[string]any
	for _, value := range blocks {
		block, ok := value.(map[string]any)
		if !ok || block["type"] != "tool_use" {
			continue
		}
		name, _ := block["name"].(string)
		id, _ := block["id"].(string)
		alias, found := strings.CutPrefix(name, "mcp__tokenhub__")
		var tool claudeSubscriptionTool
		if found {
			for _, candidate := range r.tools {
				if candidate.Name == alias {
					tool = candidate
					break
				}
			}
		}
		if tool.Name == "" || id == "" {
			return nil, NewHTTPError(502, "claude_subscription_tool_refused", "Claude Code attempted an undeclared tool")
		}
		pending := r.internal[id]
		if pending == nil {
			pending = &claudeSubscriptionPending{internalID: id, ready: make(chan struct{}), result: make(chan string, 1)}
			r.internal[id] = pending
		}
		if pending.registered {
			continue
		}
		arguments, ok := block["input"].(map[string]any)
		if !ok {
			return nil, NewHTTPError(502, "claude_subscription_tool_invalid", "Claude Code returned invalid tool arguments")
		}
		external, err := randomHex(24)
		if err != nil {
			return nil, err
		}
		pending.externalID = "call_" + external
		pending.tool = tool
		pending.arguments = arguments
		pending.registered = true
		r.waiting[pending.externalID] = pending
		r.adapter.mu.Lock()
		r.adapter.calls[pending.externalID] = r
		r.adapter.mu.Unlock()
		close(pending.ready)
		encoded, _ := json.Marshal(arguments)
		calls = append(calls, map[string]any{"id": pending.externalID, "type": "function", "function": map[string]any{"name": tool.OriginalName, "arguments": string(encoded)}})
	}
	return calls, nil
}
