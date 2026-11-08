package holmes

import (
	"encoding/json"
	"strings"
)

// sanitizeHistory removes model reasoning ("thinking") from a conversation history before it is
// sent back. Anthropic binds signed thinking blocks to the exact original prompt; replaying them
// under a different system prompt (chat vs investigation) is rejected. Answers and tool results
// are kept, so the agent keeps its context.
func sanitizeHistory(raw json.RawMessage) json.RawMessage {
	var msgs []map[string]any
	if err := json.Unmarshal(raw, &msgs); err != nil {
		return raw
	}
	for _, m := range msgs {
		delete(m, "thinking_blocks")
		delete(m, "reasoning_content")
		delete(m, "reasoning")
		if parts, ok := m["content"].([]any); ok {
			kept := parts[:0]
			for _, p := range parts {
				if pm, ok := p.(map[string]any); ok {
					if t, _ := pm["type"].(string); t == "thinking" || t == "redacted_thinking" {
						continue
					}
				}
				kept = append(kept, p)
			}
			m["content"] = kept
		}
	}
	out, err := json.Marshal(msgs)
	if err != nil {
		return raw
	}
	return out
}

// historyRejected reports provider errors caused by the replayed history itself.
func historyRejected(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "thinking") || strings.Contains(s, "signature") || strings.Contains(s, "tool_use_id") ||
		(strings.Contains(s, "messages.") && strings.Contains(s, "invalid_request_error"))
}
