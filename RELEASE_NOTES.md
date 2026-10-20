## What's Changed

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
