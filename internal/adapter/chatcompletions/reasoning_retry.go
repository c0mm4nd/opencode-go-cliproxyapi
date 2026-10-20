package chatcompletions

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
)

// MissingReasoningRetry prepares one compatibility retry only when the
// upstream explicitly rejects missing reasoning_content. Mixed histories may
// contain tool calls made without thinking, or thinking a client did not retain.
// A blank placeholder acknowledges that absence; it does not reconstruct reasoning.
// A single space survives relays that discard empty strings.
// Existing strings (including empty ones) and non-tool messages stay untouched.
func MissingReasoningRetry(status int, response, request []byte) ([]byte, bool) {
	if status != http.StatusBadRequest {
		return nil, false
	}
	var failure struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(response, &failure) != nil || failure.Error.Type != "invalid_request_error" {
		return nil, false
	}
	message := strings.ToLower(failure.Error.Message)
	if !strings.Contains(message, "reasoning_content") ||
		!strings.Contains(message, "thinking mode") ||
		!strings.Contains(message, "must be passed back") {
		return nil, false
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(request, &body) != nil || body == nil {
		return nil, false
	}
	var messages []json.RawMessage
	if json.Unmarshal(body["messages"], &messages) != nil {
		return nil, false
	}
	changed := false
	for i, raw := range messages {
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) != nil || m == nil {
			continue
		}
		var role string
		var calls []json.RawMessage
		if json.Unmarshal(m["role"], &role) != nil || role != "assistant" ||
			json.Unmarshal(m["tool_calls"], &calls) != nil || len(calls) == 0 {
			continue
		}
		if value, exists := m["reasoning_content"]; exists && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			continue
		}
		m["reasoning_content"] = json.RawMessage(`" "`)
		messages[i], _ = json.Marshal(m)
		changed = true
	}
	if !changed {
		return nil, false
	}
	body["messages"], _ = json.Marshal(messages)
	retry, err := json.Marshal(body)
	return retry, err == nil
}
