package plugin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestExecutorMissingReasoningRetry(t *testing.T) {
	const failure = `{"error":{"type":"invalid_request_error","message":"The reasoning_content in the thinking mode must be passed back to the API."}}`
	const source = `{"model":"alias","thinking":{"type":"enabled"},"messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"one","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","tool_call_id":"one","content":"ok"}]}`
	for _, stream := range []bool{false, true} {
		for _, secondFails := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/secondFails=%t", stream, secondFails), func(t *testing.T) {
				f := &fakeCaller{}
				m := NewManager(NewHostBridge(f.call))
				t.Cleanup(func() { _, _ = m.HandleCall("plugin.shutdown", nil) })
				var requests []pluginapi.HTTPRequest
				reads := 0
				f.responder = wrapWithCatalog(multiRouteCatalog, func(method string, payload []byte) ([]byte, error) {
					switch method {
					case pluginabi.MethodHostHTTPDo, pluginabi.MethodHostHTTPDoStream:
						var req pluginapi.HTTPRequest
						if err := json.Unmarshal(payload, &req); err != nil {
							t.Fatal(err)
						}
						requests = append(requests, req)
						if len(requests) > 2 {
							t.Fatal("more than one retry")
						}
						status := http.StatusOK
						body := ccResponseBody
						if len(requests) == 1 || secondFails {
							status = 400
							body = failure
						}
						if stream {
							return hostOK(hostStreamStartResp{StatusCode: status, StreamID: fmt.Sprintf("up-%d", len(requests))}), nil
						}
						return hostOK(pluginapi.HTTPResponse{StatusCode: status, Body: []byte(body)}), nil
					case pluginabi.MethodHostHTTPStreamRead:
						if len(requests) == 1 || secondFails {
							// A real error body may arrive in separate reads.
							reads++
							half := len(failure) / 2
							if reads%2 == 1 {
								return hostOK(hostStreamReadResp{Payload: []byte(failure[:half])}), nil
							}
							return hostOK(hostStreamReadResp{Payload: []byte(failure[half:]), Done: true}), nil
						}
						return hostOK(hostStreamReadResp{Payload: []byte("data: " + ccResponseBody + "\n\ndata: [DONE]\n\n"), Done: true}), nil
					}
					return hostOK(map[string]any{}), nil
				})
				if _, err := m.HandleCall("plugin.register", lifecycleRequestBody(testValidYAML)); err != nil {
					t.Fatal(err)
				}
				var response []byte
				var err error
				if stream {
					response, err = m.HandleCall("executor.execute_stream", execStreamReqBody("opencode-go/glm-5.3", "openai", []byte(source), "down-1"))
				} else {
					response, err = m.HandleCall("executor.execute", execReqBody("opencode-go/glm-5.3", "openai", []byte(source), false))
				}
				if err != nil {
					t.Fatal(err)
				}
				env := decodeEnv(t, response)
				if env.OK == secondFails {
					t.Fatalf("unexpected result: %s", response)
				}
				if secondFails && (env.Error == nil || env.Error.HTTPStatus != 400) {
					t.Fatalf("lost second rejection: %s", response)
				}
				if len(requests) != 2 {
					t.Fatalf("attempts=%d", len(requests))
				}
				if strings.Contains(string(requests[0].Body), "reasoning_content") || !strings.Contains(string(requests[1].Body), `"reasoning_content":" "`) {
					t.Fatal("incorrect original or retried body")
				}
				if !reflect.DeepEqual(requests[0].Headers, requests[1].Headers) || requests[0].URL != requests[1].URL {
					t.Fatal("changed auth, session or target")
				}
				if stream && secondFails && len(f.callsOf(pluginabi.MethodHostStreamEmit)) != 0 {
					t.Fatal("emitted before successful response")
				}
				if stream && len(f.callsOf(pluginabi.MethodHostHTTPStreamClose)) == 0 {
					t.Fatal("failed stream leaked")
				}
			})
		}
	}
}
