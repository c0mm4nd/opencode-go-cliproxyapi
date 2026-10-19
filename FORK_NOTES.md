# Claude Code image tool-result support

This fork fixes HTTP 400 errors when Claude Code reads an image using a tool and the request is routed through the OpenCode Go Chat Completions adapter:

```text
unsupported tool_result block type "image"; tool messages carry text only
```

## Behavior

Chat Completions tool messages carry text. The adapter retains each tool reply and its call ID, then places the original images in a user message labeled with the source tool call ID. All parallel tool replies precede that user message. Text and error flags remain present, and unsupported content types or malformed image sources still produce explicit errors. Session hashing also accepts image tool results.

No OCR, image removal, or model substitution is involved. The upstream model must support image input. This patch changes the Claude-to-Chat-Completions route; it does not extend the other adapters.

## Install

Download the Linux amd64 shared library and `SHA256SUMS` from this fork's GitHub release, verify the checksum, back up the installed library, and replace `plugins/linux/amd64/opencode-go-cliproxyapi.so`. Restart CLIProxyAPI to load it. For Docker deployments, replace the file in the host's mounted plugins directory and restart the CPA container.

The provided binary was built with Go 1.26.8, requires Linux amd64 and glibc 2.34 or newer, and was verified with CPA v8.0.15 on Debian 12. It is not an Alpine/musl binary. Its adapter implementation matches this branch; subsequent publication-only changes add documentation and streaming regression coverage.

## Validation

- Full package test suite passed before deployment.
- A real Anthropic Messages request containing an image tool result returned HTTP 200 and correctly identified a two-color test image.
- Claude Code invoked Read, received an image tool result, and correctly identified the left and right colors through the fallback gateway route.
- Publication checks use `go test -tags debug ./...` and `go vet ./...`.

For local development, follow CONTRIBUTING.md and use the debug build tag. Release build command:

```sh
CGO_ENABLED=1 go build -trimpath -buildmode=c-shared -ldflags "-s -w" -o opencode-go-cliproxyapi.so .
```

Keep the backup for rollback. Check whether future upstream releases contain this fix before replacing the fork's library.
