## Fork image tool-result support (based on v0.2.1)

- Preserve Claude image tool results on Chat Completions routes, including parallel tools and session derivation.
- Retain upstream `tool_reference` serialization for mixed text, reference, and image results.
- Add offline request and executor tests for streaming and non-streaming calls.

## What's Changed

### Bug Fixes

- Fix `tool_result` translation when receiving `tool_reference` content blocks from Claude clients (such as ToolSearch, web search, or web fetch in Claude Code), preventing 500 errors and conversation lockouts on Chat Completions and Responses models like DeepSeek.

Special thanks to @SpiGAndromeda in #14.

## Upgrade Notes

- Replace the old plugin binary with the new release binary.
- Restart CLIProxyAPI after replacing the plugin.

**Full Changelog**: https://github.com/massiveits/opencode-go-cliproxyapi/compare/v0.2.0...v0.2.1
