# Contributing

Thanks for helping improve Pig AI Agent Kit. Keep changes focused on the public
MCP client, its tests, the Pig AI Skill, or user-facing integration examples.
The Pig AI backend and provider adapters are not part of this repository.

## Before opening a pull request

1. Create a focused branch from the current default branch.
2. Add or update tests for observable behavior, especially authentication
   redaction, URL validation, idempotency, multipart inputs, and downloads.
3. Use local HTTP fixtures for client tests. Do not call the production API or
   submit billable generation from automated tests.
4. Run:

   ```bash
   go test ./...
   go vet ./...
   go build ./cmd/pig-ai-mcp
   ```

5. Review the diff for API keys, credentials, prompts, generated media, local
   paths, private URLs, and unrelated generated files.

## Design expectations

- Keep the MCP process a thin public API adapter. Do not add provider routing,
  billing, settlement, or storage logic to this repository.
- Preserve the eight public MCP tool names and their documented semantics, or
  explain a compatibility change explicitly.
- Keep stdout reserved for MCP protocol frames. Diagnostics belong on stderr
  and must redact credentials.
- Default to the official HTTPS API origin. A custom origin must remain an
  explicit opt-in, and remote plain HTTP must remain rejected.
- Use the smallest implementation that meets the public contract; avoid new
  dependencies unless they provide a clear current benefit.
- Keep `skills/pig-ai/SKILL.md` concise and put detailed interface material in
  `skills/pig-ai/references/api.md`.

## Pull requests

Describe what changed, why it is needed, and how it was verified. Keep
refactors separate from behavior changes when practical. By contributing, you
agree that your contribution is licensed under the Apache License 2.0.

For security issues, follow [SECURITY.md](SECURITY.md) instead of opening a
public pull request or issue.
