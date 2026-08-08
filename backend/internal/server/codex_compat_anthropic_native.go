package server

// nativeAnthropicPayload copies the mutable message envelope and removes
// provider-tagged reasoning that Anthropic did not mint. A Messages request can
// fail over from a Codex subscription to a native Anthropic route; forwarding a
// Codex encrypted continuation as an Anthropic thinking signature would make
// the fallback fail even though the user and tool content is portable.
func nativeAnthropicPayload(raw map[string]any) map[string]any {
	payload := cloneAnyMap(raw)
	messages, ok := anySlice(raw["messages"])
	if !ok {
		return payload
	}
	copied := make([]any, 0, len(messages))
	for _, rawMessage := range messages {
		message, ok := rawMessage.(map[string]any)
		if !ok {
			copied = append(copied, rawMessage)
			continue
		}
		next := cloneAnyMap(message)
		blocks, blocksOK := anySlice(message["content"])
		if !blocksOK {
			copied = append(copied, next)
			continue
		}
		content := make([]any, 0, len(blocks))
		for _, rawBlock := range blocks {
			block, ok := rawBlock.(map[string]any)
			if !ok || block["type"] != "thinking" {
				content = append(content, rawBlock)
				continue
			}
			signature, _ := block["signature"].(string)
			if decoded, valid := decodeProviderSignature(anthropicSignatureProvider, signature); valid {
				nextBlock := cloneAnyMap(block)
				nextBlock["signature"] = decoded
				content = append(content, nextBlock)
				continue
			}
			if foreignProviderSignature(signature) {
				continue
			}
			content = append(content, rawBlock)
		}
		next["content"] = content
		copied = append(copied, next)
	}
	payload["messages"] = copied
	return payload
}

func foreignProviderSignature(signature string) bool {
	for _, provider := range []string{codexSignatureProvider, geminiSignatureProvider, reasoningSignatureProvider} {
		if _, tagged := decodeProviderSignature(provider, signature); tagged {
			return true
		}
	}
	return false
}
