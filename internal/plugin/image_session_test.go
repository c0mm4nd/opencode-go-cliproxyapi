package plugin

import "testing"

func TestSessionHashAcceptsToolImages(t *testing.T) {
	request := func(data string) []byte {
		return []byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","content":[{"type":"text","text":"image"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + data + `"}}]}]}]}`)
	}
	a, err := deriveOpenCodeSessionID("claude", request("QUJD"))
	if err != nil || a == "" {
		t.Fatalf("image result rejected: %v", err)
	}
	b, err := deriveOpenCodeSessionID("claude", request("REVG"))
	if err != nil || a == b {
		t.Fatalf("image not represented in session: %v", err)
	}
}
