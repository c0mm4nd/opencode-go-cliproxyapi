package chatcompletions

import (
	"encoding/json"
	"strings"
	"testing"
)

const missingReasoningError = `{"error":{"type":"invalid_request_error","message":"The ` + "`reasoning_content`" + ` in the thinking mode must be passed back to the API."}}`

func TestMissingReasoningRetry(t *testing.T) {
	request := []byte(`{"model":"any-alias","thinking":{"type":"enabled"},"messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"one"}],"custom":42},{"role":"assistant","tool_calls":[{"id":"two"}],"reasoning_content":"original\n思考"},{"role":"assistant","tool_calls":[{"id":"three"}],"reasoning_content":""},{"role":"assistant","tool_calls":[{"id":"four"}],"reasoning_content":null},{"role":"assistant","content":"text only"},{"role":"tool","tool_call_id":"one","content":"answer"}]}`)
	before := string(request)
	got, ok := MissingReasoningRetry(400, []byte(missingReasoningError), request)
	if !ok {
		t.Fatal("no compatibility retry")
	}
	var body struct {
		Model    string
		Thinking map[string]string
		Messages []map[string]any
	}
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{0, 3} {
		if v, exists := body.Messages[i]["reasoning_content"]; !exists || v != " " {
			t.Fatalf("message %d = %v", i, body.Messages[i])
		}
	}
	if body.Messages[2]["reasoning_content"] != "" {
		t.Fatal("changed existing empty string")
	}
	if body.Messages[1]["reasoning_content"] != "original\n思考" || body.Messages[0]["custom"] != float64(42) {
		t.Fatal("changed existing data")
	}
	for _, i := range []int{4, 5} {
		if _, exists := body.Messages[i]["reasoning_content"]; exists {
			t.Fatal("modified non-tool assistant or tool result")
		}
	}
	if body.Model != "any-alias" || body.Thinking["type"] != "enabled" || string(request) != before {
		t.Fatal("changed model, mode or original request")
	}
	if _, ok := MissingReasoningRetry(400, []byte(missingReasoningError), got); ok {
		t.Fatal("retry without missing fields")
	}
}

func TestMissingReasoningRetryGates(t *testing.T) {
	request := `{"messages":[{"role":"assistant","tool_calls":[{}]}]}`
	for _, tc := range []struct {
		name              string
		status            int
		response, request string
	}{
		{"rate limit", 429, missingReasoningError, request},
		{"server error", 500, missingReasoningError, request},
		{"success", 200, missingReasoningError, request},
		{"other error type", 400, strings.Replace(missingReasoningError, "invalid_request_error", "authentication_error", 1), request},
		{"other validation", 400, `{"error":{"type":"invalid_request_error","message":"reasoning_content must be a string"}}`, request},
		{"not JSON", 400, "bad", request},
		{"not envelope", 400, `{"message":"reasoning_content in the thinking mode must be passed back"}`, request},
		{"invalid request", 400, missingReasoningError, "bad"},
		{"no messages", 400, missingReasoningError, `{}`},
		{"no tool calls", 400, missingReasoningError, `{"messages":[{"role":"assistant","content":"x"}]}`},
		{"malformed existing reasoning", 400, missingReasoningError, `{"messages":[{"role":"assistant","tool_calls":[{}],"reasoning_content":42}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := MissingReasoningRetry(tc.status, []byte(tc.response), []byte(tc.request)); ok {
				t.Fatal("unexpected retry")
			}
		})
	}
}
