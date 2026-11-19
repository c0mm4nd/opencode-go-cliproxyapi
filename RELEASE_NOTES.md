## What's Changed

### Account-wide concurrency and 5h quota display

- Fork `0.2.2-account-concurrency.1`: apply the configurable account/group
  concurrency gate to every model, including native Messages and Responses
  routes. Rename settings to `max-in-flight` and `queue-timeout`; the old
  GLM-specific settings are removed and must be migrated.
- Publish upstream `usage.rolling` as the explicit `5h` quota window, keeping
  its remaining fraction and reset time so CPA's generic quota card displays
  the five-hour allowance and timeline. Weekly/monthly readings remain intact.
- Upstream main `e8d3e36` (v0.2.2) was already included at the merge check.

### GLM account concurrency

- Fork `0.2.2-glm-concurrency.1`: enforce six concurrent GLM requests per selected
  account by default, sharing slots across models/protocols. Excess requests
  queue for up to 30 seconds. Optional `concurrency-group` combines keys from
  one workspace. Streaming and timed-out host calls retain slots until actual
  upstream cleanup finishes. See `FORK_NOTES.md` for scope and cancellation limits.

### Bug Fixes

- Fork `0.2.2-reasoning.2`: retry once when a Chat Completions upstream explicitly rejects missing thinking history. Only missing/null reasoning fields on assistant tool calls receive single-space placeholders; original reasoning and thinking mode remain unchanged. Covers streaming and non-streaming, closes rejected streams, and preserves the original deadline.

- Preserve Chat Completions `reasoning_content` as Claude thinking blocks in streaming and non-streaming responses, and restore it when replaying assistant history with tool results. This prevents thinking-mode continuation failures caused by dropped reasoning. Explicit enabled/disabled thinking controls are forwarded on DeepSeek Chat Completions routes. Encrypted redacted thinking remains omitted; previously lost history cannot be reconstructed.

- Preserve Claude image tool results when routing to Chat Completions: retain images in an associated user message after tool replies, preserve text, tool references and error markers, and include images in session derivation. The selected upstream model must support image input.

Special thanks to @c0mm4nd in #16.

- Fix `tool_result` translation when receiving `tool_reference` content blocks from Claude clients (such as ToolSearch, web search, or web fetch in Claude Code), preventing 500 errors and conversation lockouts on Chat Completions and Responses models like DeepSeek.

Special thanks to @SpiGAndromeda in #14.

## Upgrade Notes

- Replace the old plugin binary with the new release binary.
- Restart CLIProxyAPI after replacing the plugin.

**Full Changelog**: https://github.com/massiveits/opencode-go-cliproxyapi/compare/v0.2.1...v0.2.2
