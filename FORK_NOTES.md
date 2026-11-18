# OpenCode Go fork changes

## GLM account concurrency

Fork version `0.2.2-glm-concurrency.1` retains the reasoning fixes below and
defaults to at most six simultaneous GLM requests per selected API key. All
upstream model IDs starting with `glm-` share that account's slots, across
Chat Completions, Messages, and Responses clients. Other model families are
unchanged. `glm-max-in-flight` accepts any non-negative integer, with 0 disabling the limit; the default
`glm-queue-timeout` is 30 seconds. Queuing counts toward `request-timeout`.
Saturated accounts queue their selected requests in FIFO order; the plugin
does not change CPA's credential selection or move sticky sessions itself.

For multiple keys in one upstream workspace, add the same `concurrency-group`
to each matching `api-keys` entry, for example:

```yaml
api-keys:
  - value: "${GO_KEY_A}"
    concurrency-group: my-workspace
  - value: "${GO_KEY_B}"
    concurrency-group: my-workspace
glm-max-in-flight: 6
glm-queue-timeout: 30s
```

Account labels and filenames are not identity. Idle counters are reclaimed;
normal reconfiguration retains active counters. Drain requests before changing
keys' group assignments or restarting/replacing the plugin. Limits are local
to this plugin instance; other CPA processes and direct upstream calls are
outside its accounting and must be coordinated separately.

Non-streaming calls keep their slot until the actual host callback completes,
even if the plugin has already returned a timeout. Streams retain a slot until
upstream close finishes, including late-start cleanup after a timeout. Missing
reasoning retries remain within the same permit. Queue deadlines and plugin
shutdown cancel pending waiters without sending upstream requests. Stream
disconnect/emit failures close upstream before releasing the slot. The pinned
native executor ABI does not expose a client cancellation context while a call
is queued: an early client disconnect may therefore remain queued until its
queue deadline or until admitted and the host observes the disconnect. This
does not release active upstream slots early.

Offline tests cover mixed streaming/non-streaming admission, independent
accounts, shared workspace/model groups, reconfiguration, cancellation, and
callbacks/streams that finish after their deadlines. Concurrent integration
verification should use a local upstream fixture; avoid intentionally exceeding
the production service's concurrency allowance to test this limit.

## Claude Code image tool-result support

The image tool-result fix from this fork was merged upstream in [PR #16](https://github.com/massiveits/opencode-go-cliproxyapi/pull/16) and is included in **v0.2.2**. This branch now incorporates upstream v0.2.2, including the maintainer's live regression test and updated contribution guidelines. The fork now additionally preserves reasoning content across Claude/Chat Completions tool turns; this newer fix is not yet part of upstream v0.2.2.

## Installation

The official v0.2.2 release contains the image fix but not the newer reasoning round-trip fix. For the latter, use the current fork build. Build the current fork with the `debug` tag as described in CONTRIBUTING.md and verify the artifact checksum. Back up the installed library before replacing it, then restart CLIProxyAPI. In Docker, replace the library in the host's mounted plugins directory.

The earlier fork releases `v0.1.10-image-tools.1` and `v0.2.0-image-tools.1` remain available as historical artifacts. They are no longer needed to obtain the image fix, and neither includes the newer reasoning fix.

## Behavior and compatibility

The Claude-to-Chat-Completions adapter retains each tool reply and its call ID, then places original image data/URLs in a following user message labeled with the originating tool call ID. All parallel tool replies precede that user message. Text, error flags, and upstream `tool_reference` serialization are retained; session derivation also accepts images. Invalid image sources and unsupported blocks still fail explicitly.

The fix has no model-name-specific behavior. The upstream model must support image input. Native Messages and Responses conversion paths are outside this change's scope. No OCR, image deletion, or model substitution is performed.

## Validation and development

The contribution was verified with offline tests, `go vet`, and a debug native build, then exercised in an isolated CPA v8.0.33 instance against the real `deepseek-v4.1-flash` upstream with other-provider fallback disabled. Streaming and non-streaming image requests, mixed references/images, parallel tool results, structured tool calls, and subsequent tool-result replies passed. Claude Code completed a real Read image invocation and correctly identified the fixture's red/blue halves. Live model coverage was limited to DeepSeek.

Follow CONTRIBUTING.md for current requirements. Local development uses the `debug` build tag; release artifacts come from upstream CI. Upstream v0.2.2 also requires a live regression test for bug fixes, in addition to offline unit tests.

## Reasoning tool-turn compatibility

The fork maps upstream `reasoning_content` to plaintext Claude thinking blocks, then maps those blocks back to `reasoning_content` in subsequent assistant history. Both streaming and non-streaming paths preserve the original text, including fragmented streams and parallel tool calls. No thinking is invented, and encrypted redacted thinking is not decoded. Already-dropped reasoning cannot be recovered. Version `0.2.2-reasoning.2` handles mixed histories with a bounded compatibility retry, described below.

For DeepSeek Chat Completions targets, explicit Claude `thinking.type` values `enabled` and `disabled` also set the upstream thinking mode. Effort alone does not enable that mode. Other model families do not receive this provider-specific extension; reasoning data preservation itself is model independent.

The reasoning fix passed the full debug test suite and go vet. Against a real Go upstream with other-provider fallback disabled, both streaming and non-streaming thinking/tool-result continuations passed. Claude Code also retained two thinking blocks, completed Read on an image, and returned the correct colors. These live checks used deepseek-v4.1-flash and CPA v8.0.33.

### Mixed histories with missing thinking

A conversation can contain tool calls with no retained thinking even when later turns preserve it correctly. If a Chat Completions upstream returns HTTP 400 with `invalid_request_error` explicitly requiring `reasoning_content` to be passed back in thinking mode, the executor retries once with single-space placeholders for missing or null `reasoning_content` on assistant tool-call messages. Existing reasoning, tool IDs/results, model, thinking controls, authentication, and session headers are preserved. Blank placeholders acknowledge unavailable history; they do not recover the original reasoning. A single space is used because the tested Go route rejected empty-string replay while accepting a blank placeholder.

The retry is triggered by the actual validation response, without a model-name check. Other errors and protocol routes are unchanged. Both attempts share the request deadline; rejected streams are closed before retrying, and no response bytes are emitted before success. A second failure is returned normally. This can add one failed request to latency when the client continues sending incomplete history. The compatibility retry is logged without conversation content.

`TestLiveClaudeMissingReasoningHistory` covers streaming and non-streaming mixed-history continuations. Live validation remains limited to DeepSeek through OpenCode Go; this is a fork compatibility fix, not a claim of universal upstream support.
