# Claude Code image tool-result support

The image tool-result fix from this fork was merged upstream in [PR #16](https://github.com/massiveits/opencode-go-cliproxyapi/pull/16) and is included in **v0.2.2**. This branch now incorporates upstream v0.2.2, including the maintainer's live regression test and updated contribution guidelines. There are no fork-only Go code changes relative to that upstream version.

## Installation

Use the [official upstream v0.2.2 release](https://github.com/massiveits/opencode-go-cliproxyapi/releases/tag/v0.2.2), select the package for your platform, and verify it against the supplied checksums. Back up the installed library before replacing it, then restart CLIProxyAPI. In Docker, replace the library in the host's mounted plugins directory.

The earlier fork releases `v0.1.10-image-tools.1` and `v0.2.0-image-tools.1` remain available as historical artifacts. They are no longer needed to obtain this fix.

## Behavior and compatibility

The Claude-to-Chat-Completions adapter retains each tool reply and its call ID, then places original image data/URLs in a following user message labeled with the originating tool call ID. All parallel tool replies precede that user message. Text, error flags, and upstream `tool_reference` serialization are retained; session derivation also accepts images. Invalid image sources and unsupported blocks still fail explicitly.

The fix has no model-name-specific behavior. The upstream model must support image input. Native Messages and Responses conversion paths are outside this change's scope. No OCR, image deletion, or model substitution is performed.

## Validation and development

The contribution was verified with offline tests, `go vet`, and a debug native build, then exercised in an isolated CPA v8.0.33 instance against the real `deepseek-v4.1-flash` upstream with other-provider fallback disabled. Streaming and non-streaming image requests, mixed references/images, parallel tool results, structured tool calls, and subsequent tool-result replies passed. Claude Code completed a real Read image invocation and correctly identified the fixture's red/blue halves. Live model coverage was limited to DeepSeek.

Follow CONTRIBUTING.md for current requirements. Local development uses the `debug` build tag; release artifacts come from upstream CI. Upstream v0.2.2 also requires a live regression test for bug fixes, in addition to offline unit tests.
