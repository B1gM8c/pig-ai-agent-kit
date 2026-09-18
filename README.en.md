# Pig AI Agent Kit

[简体中文](README.md) | English

Pig AI Agent Kit is a fully independent, open-source client toolkit that lets MCP-capable clients such as Codex, Claude Desktop, and Cursor generate images and videos through the public Pig AI API.

> **Rollout status:** The [`v0.1.0-beta.2`](https://github.com/B1gM8c/pig-ai-agent-kit/releases/tag/v0.1.0-beta.2) pre-release provides downloadable MCP, Skill, and checksum assets. The production API is available at the public Base URL `https://www.pigsec.cn/openapi/v1/ai`, and all public endpoints use `X-API-Key` authentication.

This repository contains only:

- `pig-ai-mcp`, a local stdio MCP client;
- the `pig-ai` Skill, which describes a safe agent workflow and the public tool contracts;
- minimal Codex, Claude Desktop, and Cursor configuration examples.

It does **not** contain the Pig AI website, backend, billing or points ledger, model-provider adapters, or provider credentials. Generation, point charges and refunds, call auditing, and result storage all remain server-side. The MCP server is only a thin public API client.

There is no separate "agent executable" to download. `skills/pig-ai/agents/openai.yaml` is UI metadata for the Skill. For now, Skill-capable hosts can install `skills/pig-ai` from source and connect a locally built `pig-ai-mcp`; other MCP hosts can use the eight tools directly.

## Quick start

The production flow is: create an API key → obtain MCP → configure the environment and host → install the Skill → verify with `list_models`. The public Base URL is `https://www.pigsec.cn/openapi/v1/ai`; MCP uses `https://www.pigsec.cn` as its API origin and appends that path automatically.

### 1. Create an API key

Sign in to the [Pig AI developer center](https://www.pigsec.cn/ai/developer/keys), create an API key, and grant only the scopes you need. The complete secret is shown once, so save it in a password manager immediately.

Image and video generation uses the same server-side Pig AI points account as the web application. Use `preview_billing` before submission when you need an estimate. Final charges, refunds, and usage records are determined by the server ledger.

### 2. Download or build MCP

Download the [`v0.1.0-beta.2`](https://github.com/B1gM8c/pig-ai-agent-kit/releases/tag/v0.1.0-beta.2) pre-release for macOS, Linux, or Windows on amd64/arm64. It also includes a source archive and [`checksums.txt`](https://github.com/B1gM8c/pig-ai-agent-kit/releases/download/v0.1.0-beta.2/checksums.txt). Verify the SHA-256 checksum before extracting the archive, then configure your host with the absolute path to `pig-ai-mcp` (`pig-ai-mcp.exe` on Windows).

You can also build from source with Go 1.25.14 or newer:

```bash
git clone https://github.com/B1gM8c/pig-ai-agent-kit.git
cd pig-ai-agent-kit
mkdir -p bin
go build -o ./bin/pig-ai-mcp ./cmd/pig-ai-mcp
```

On Windows PowerShell, build an executable with the `.exe` suffix:

```powershell
New-Item -ItemType Directory -Force .\bin | Out-Null
go build -o .\bin\pig-ai-mcp.exe .\cmd\pig-ai-mcp
```

`releases/latest` resolves only to a stable release, not to this pre-release. Use the fixed version link above until the first stable release is published.

### 3. Configure the API key

macOS / Linux:

```bash
export PIGAI_API_KEY="<YOUR_PIGAI_API_KEY>"
```

Windows PowerShell:

```powershell
$env:PIGAI_API_KEY = "<YOUR_PIGAI_API_KEY>"
```

Prefer supplying the API key through the host process's `PIGAI_API_KEY` environment variable. Never put a real key in a repository, shared configuration, chat, command-line argument, or screenshot. The public Base URL is `https://www.pigsec.cn/openapi/v1/ai`; `PIGAI_BASE_URL` remains the API origin `https://www.pigsec.cn`, and MCP appends `/openapi/v1/ai` automatically.

Environment variables:

| Variable | Default | Description |
| --- | --- | --- |
| `PIGAI_API_KEY` | none | Required Pig AI developer API key. |
| `PIGAI_DOWNLOAD_DIR` | `pig-ai-mcp/downloads` under the user cache directory | Result directory; must be an absolute path. |
| `PIGAI_HTTP_TIMEOUT` | `60s` | Per-request HTTP timeout, up to `10m`. |
| `PIGAI_MAX_DOWNLOAD_BYTES` | `1073741824` | Maximum bytes for one downloaded result. |
| `PIGAI_BASE_URL` | `https://www.pigsec.cn` | API origin; MCP appends `/openapi/v1/ai`. The public Base URL is `https://www.pigsec.cn/openapi/v1/ai`. |
| `PIGAI_ALLOW_CUSTOM_BASE_URL` | `false` | Explicit opt-in for a custom `PIGAI_BASE_URL`. Remote URLs must use HTTPS; HTTP is limited to loopback test fixtures. |

### 4. Connect an MCP host

Always use the MCP binary's **absolute path**.

#### Codex

Merge [examples/codex-config.toml](examples/codex-config.toml) into `~/.codex/config.toml`:

```toml
[mcp_servers.pig-ai]
command = "/ABSOLUTE/PATH/TO/pig-ai-mcp"
env_vars = ["PIGAI_API_KEY"]
```

`env_vars` forwards the variable from the environment that launched Codex instead of persisting the key in the configuration file. Restart Codex and use `/mcp` to inspect the connection.

#### Claude Desktop

The preferred Claude Desktop distribution format is an `.mcpb` package with the API key declared as sensitive. **This project does not currently publish an `.mcpb` artifact.** Until one is available, use [examples/claude-desktop.json](examples/claude-desktop.json) as a manual fallback and replace the placeholder locally:

```json
{
  "mcpServers": {
    "pig-ai": {
      "command": "/ABSOLUTE/PATH/TO/pig-ai-mcp",
      "env": {
        "PIGAI_API_KEY": "<YOUR_PIGAI_API_KEY>"
      }
    }
  }
}
```

This fallback stores the key as plaintext in the local Claude configuration, so use it only on a protected personal device. Do not commit, share, sync, or back up that file; restrict its file permissions. Create a separate key with only the exact required scopes and rotate it regularly. Fully quit and reopen Claude Desktop after editing it.

#### Cursor

Save [examples/cursor-mcp.json](examples/cursor-mcp.json) as `.cursor/mcp.json` for one project or `~/.cursor/mcp.json` for all projects:

```json
{
  "mcpServers": {
    "pig-ai": {
      "type": "stdio",
      "command": "/ABSOLUTE/PATH/TO/pig-ai-mcp",
      "env": {
        "PIGAI_API_KEY": "${env:PIGAI_API_KEY}"
      }
    }
  }
}
```

`${env:PIGAI_API_KEY}` reads the environment actually inherited by the Cursor process. Launch Cursor from an environment where the variable is set. A temporary shell `export` is not inherited when Cursor is later opened from the Dock, Start menu, or another unrelated launcher.

### 5. Install the Skill

Download [`pig-ai-skill.zip`](https://github.com/B1gM8c/pig-ai-agent-kit/releases/download/v0.1.0-beta.2/pig-ai-skill.zip) and extract the installable `pig-ai` directory, or install it from a cloned checkout:

```bash
mkdir -p "$HOME/.agents/skills"
cp -R skills/pig-ai "$HOME/.agents/skills/"
```

Windows PowerShell:

```powershell
New-Item -ItemType Directory -Force "$HOME\.agents\skills" | Out-Null
Copy-Item -Recurse -Force "skills\pig-ai" "$HOME\.agents\skills"
```

The pre-release provides both `pig-ai-skill.zip` and the versioned `pig-ai-skill_0.1.0-beta.2.zip` archive. Use the fixed version URL above until the first stable release is published.

For a repository-scoped Skill, copy `skills/pig-ai` to `.agents/skills/pig-ai` in that repository. Skill changes are normally detected automatically; restart the host if it does not appear.

### 6. Verify

Restart the MCP host and verify the connection. In Codex with the Skill installed, send:

```text
Use $pig-ai to call list_models. Do not create any image or video.
```

In a plain MCP host without the Skill, call the `list_models` tool from the `pig-ai` server directly. Do not use `$pig-ai`; that is the Skill name, not an MCP tool name.

A successful response lists current image and video capabilities. `list_models` does not create a generation operation. A `401` or `403` means the key or its `ai:models` scope needs attention.

## MCP tools

| Tool | Purpose |
| --- | --- |
| `list_models` | Read enabled models, capabilities, and controls. |
| `preview_billing` | Estimate points, free allowance, and balance impact. |
| `create_image` | Idempotently submit an asynchronous image operation. |
| `create_video` | Idempotently submit an asynchronous video operation. |
| `get_operation` | Read generation, timing, result, and settlement state. |
| `download_result` | Download one result to a private local directory. |
| `ensure_segments` | Idempotently request image segmentation metadata. |
| `get_segments` | Read persisted segmentation status and bbox data. |

See the [Skill API contract](skills/pig-ai/references/api.md) for exact arguments and semantics. The public REST reference is available in the [Pig AI developer documentation](https://www.pigsec.cn/ai/developer/docs).

## Security and privacy

- The MCP client sends requests only to the configured Pig AI API and contains no model-provider credentials.
- The API key is supplied as `PIGAI_API_KEY` and is never returned in tool output.
- Result downloads use a private directory, a size limit, and SHA-256 hashing, then publish atomically without overwriting an existing file.
- Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md). Never paste keys, prompts, or generated results into a public issue.

## Development

```bash
go test ./...
go vet ./...
go build ./cmd/pig-ai-mcp
```

Read [CONTRIBUTING.md](CONTRIBUTING.md) before contributing.

## License

[Apache License 2.0](LICENSE)
