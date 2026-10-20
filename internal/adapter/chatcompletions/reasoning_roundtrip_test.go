package chatcompletions

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Reconstruct client-visible content, then send it back with tool results.
// This catches loss on either side of the protocol boundary, including SSE
// fragments split inside a UTF-8 sequence and consecutive parallel tool calls.
func TestClaudeReasoningToolRoundTrip(t *testing.T) {
	const reasoning = "Need the tool result.\n保持原文 ✓"
	for _, stream := range []bool{false, true} {
		for _, done := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/done=%t", stream, done), func(t *testing.T) {
				calls := []any{}
				for i := 0; i < 2; i++ {
					calls = append(calls, map[string]any{"index": i, "id": fmt.Sprintf("t%d", i), "type": "function", "function": map[string]any{"name": "lookup", "arguments": `{"x":1}`}})
				}
				var blocks []map[string]any
				if !stream {
					wire, _ := json.Marshal(map[string]any{"id": "m", "model": "test", "choices": []any{map[string]any{"message": map[string]any{"reasoning_content": reasoning, "content": "Checking.", "tool_calls": calls}, "finish_reason": "tool_calls"}}})
					result, e := ConvertNonStreamResponse("claude", 200, wire)
					if e != nil {
						t.Fatal(e)
					}
					var resp struct {
						Content []map[string]any `json:"content"`
					}
					if err := json.Unmarshal(result, &resp); err != nil {
						t.Fatal(err)
					}
					blocks = resp.Content
				} else {
					sc := NewStreamConverter("claude")
					var wire strings.Builder
					emit := func(delta map[string]any, finish string) {
						b, _ := json.Marshal(map[string]any{"id": "m", "model": "test", "choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
						fmt.Fprintf(&wire, "data: %s\n\n", b)
					}
					emit(map[string]any{"reasoning_content": "Need the tool result.\n"}, "")
					emit(map[string]any{"reasoning_content": "保持原文 ✓"}, "")
					emit(map[string]any{"content": "Checking."}, "")
					for _, c := range calls {
						emit(map[string]any{"tool_calls": []any{c}}, "")
					}
					emit(map[string]any{}, "tool_calls")
					if done {
						wire.WriteString("data: [DONE]\n\n")
					}
					var frames [][]byte
					for _, b := range []byte(wire.String()) {
						out, _, e := sc.Feed([]byte{b})
						if e != nil {
							t.Fatal(e)
						}
						frames = append(frames, out...)
					}
					if !done {
						frames = append(frames, sc.Flush()...)
					}
					open := -1
					stops := 0
					for _, ev := range parseEvents(t, frames) {
						switch ev.Name {
						case "content_block_start":
							if open != -1 {
								t.Fatal("overlapping content blocks")
							}
							open = int(ev.Data["index"].(float64))
							if open != len(blocks) {
								t.Fatal("non-contiguous block index")
							}
							blocks = append(blocks, ev.Data["content_block"].(map[string]any))
						case "content_block_delta":
							idx := int(ev.Data["index"].(float64))
							if idx != open {
								t.Fatal("delta on closed block")
							}
							d := ev.Data["delta"].(map[string]any)
							b := blocks[idx]
							switch d["type"] {
							case "thinking_delta":
								b["thinking"] = b["thinking"].(string) + d["thinking"].(string)
							case "text_delta":
								b["text"] = b["text"].(string) + d["text"].(string)
							case "input_json_delta":
								var args any
								if err := json.Unmarshal([]byte(d["partial_json"].(string)), &args); err != nil {
									t.Fatal(err)
								}
								b["input"] = args
							}
						case "content_block_stop":
							if int(ev.Data["index"].(float64)) != open {
								t.Fatal("bad block stop")
							}
							open = -1
						case "message_stop":
							stops++
						}
					}
					if open != -1 || stops != 1 {
						t.Fatalf("unfinished stream: open=%d stops=%d", open, stops)
					}
				}
				if len(blocks) != 4 || blocks[0]["type"] != "thinking" || blocks[0]["thinking"] != reasoning {
					t.Fatalf("reasoning lost: %+v", blocks)
				}
				body, _ := json.Marshal(map[string]any{"messages": []any{map[string]any{"role": "user", "content": "look up both"}, map[string]any{"role": "assistant", "content": blocks}, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "t0", "content": "first"}, map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": "second"}}}}})
				req := mustBuild(t, "claude", string(body), nil)
				msgs := req["messages"].([]any)
				a := msgs[1].(map[string]any)
				if a["reasoning_content"] != reasoning || a["content"] != "Checking." || len(a["tool_calls"].([]any)) != 2 || len(msgs) != 4 {
					t.Fatalf("history not preserved: %+v", msgs)
				}
			})
		}
	}
}

func TestClaudeReasoningHistoryPresence(t *testing.T) {
	for _, tc := range []struct {
		name, content, want string
		present             bool
	}{
		{"empty", `[{"type":"thinking","thinking":""},{"type":"text","text":"ok"}]`, "", true},
		{"multiple", `[{"type":"thinking","thinking":"a\n"},{"type":"thinking","thinking":"b"},{"type":"text","text":"ok"}]`, "a\nb", true},
		{"missing", `[{"type":"text","text":"ok"}]`, "", false},
		{"redacted", `[{"type":"redacted_thinking","data":"opaque"},{"type":"text","text":"ok"}]`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := mustBuild(t, "claude", `{"messages":[{"role":"assistant","content":`+tc.content+`}]}`, nil)
			a := m["messages"].([]any)[0].(map[string]any)
			v, ok := a["reasoning_content"]
			if ok != tc.present || (ok && v != tc.want) {
				t.Fatalf("reasoning presence changed: %+v", a)
			}
		})
	}
}

func TestClaudeThinkingOnlyStreamClosesAtFinish(t *testing.T) {
	sc := NewStreamConverter("claude")
	events := feedAll(t, sc, `data: {"id":"r","choices":[{"delta":{"reasoning_content":"check"}}]}`, `data: {"id":"r","choices":[{"delta":{},"finish_reason":"length"}]}`)
	events = append(events, parseEvents(t, sc.Flush())...)
	starts, stops, terminal := 0, 0, 0
	for _, e := range events {
		switch e.Name {
		case "content_block_start":
			starts++
			if e.Data["content_block"].(map[string]any)["type"] != "thinking" {
				t.Fatal("unexpected text block")
			}
		case "content_block_stop":
			stops++
		case "message_stop":
			terminal++
		}
	}
	if starts != 1 || stops != 1 || terminal != 1 {
		t.Fatalf("unbalanced thinking lifecycle %d/%d/%d", starts, stops, terminal)
	}
	if len(sc.Flush()) != 0 {
		t.Fatal("Flush must remain idempotent")
	}
}

func TestDeepSeekExplicitThinkingMode(t *testing.T) {
	for _, mode := range []string{"enabled", "disabled"} {
		body := []byte(`{"thinking":{"type":"` + mode + `","budget_tokens":1024},"messages":[{"role":"user","content":"hi"}]}`)
		for _, model := range []string{"deepseek-v4.1-flash", "another-model"} {
			out, e := BuildRequest(model, "claude", body, nil)
			m := decodeOut(t, out, e)
			th, ok := m["thinking"].(map[string]any)
			if model == "another-model" {
				if ok {
					t.Fatal("DeepSeek-only extension leaked to another model")
				}
				continue
			}
			if !ok || th["type"] != mode || len(th) != 1 {
				t.Fatalf("explicit mode not preserved: %+v", m)
			}
		}
	}
	out, e := BuildRequest("deepseek-v4.1-flash", "claude", []byte(`{"messages":[{"role":"user","content":"hi"}]}`), nil)
	m := decodeOut(t, out, e)
	if _, ok := m["thinking"]; ok {
		t.Fatal("missing thinking control must remain absent")
	}
}
