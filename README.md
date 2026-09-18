# Pig AI Agent Kit

简体中文 | [English](README.en.md)

Pig AI Agent Kit 是一套完全独立、开源的客户端工具，让 Codex、Claude Desktop、Cursor 等支持 MCP 的客户端通过 Pig AI 公开 API 生成图片和视频。

> **Rollout 状态：**[`v0.1.0-beta.2`](https://github.com/B1gM8c/pig-ai-agent-kit/releases/tag/v0.1.0-beta.2) 预发布版提供可下载的 MCP、Skill 和校验和。生产 API 已可用，公开 Base URL 为 `https://www.pigsec.cn/openapi/v1/ai`，所有公开接口使用 `X-API-Key` 认证。

本仓库只包含：

- `pig-ai-mcp`：本地运行的 stdio MCP 客户端；
- `pig-ai` Skill：指导 Agent 安全调用 MCP 的工作流和公开接口契约；
- Codex、Claude Desktop、Cursor 的最小配置示例。

本仓库**不包含** Pig AI 网站、后端、计费、积分账本、模型供应商适配或供应商凭据。图片/视频生成、积分扣除与退款、调用审计及结果存储都由 Pig AI 服务端处理；MCP 只是公开 API 的薄客户端。

无需再下载一个独立的“Agent 程序”：`skills/pig-ai/agents/openai.yaml` 只是 Skill 的界面元数据。当前可从源码安装 `skills/pig-ai` 并连接自行构建的 `pig-ai-mcp`；不支持 Skill 的 MCP 客户端也可以直接使用 8 个工具。

## 快速开始

生产环境的完整流程是：创建 API Key → 获取 MCP → 配置环境变量和客户端 → 安装 Skill → 调用 `list_models` 验证。公开 Base URL 为 `https://www.pigsec.cn/openapi/v1/ai`；MCP 默认使用 `https://www.pigsec.cn` 作为 API origin，并自动追加该路径。

### 1. 创建 API Key

登录 [Pig AI 开发者中心](https://www.pigsec.cn/ai/developer/keys) 创建 API Key，并按用途授予最小权限。完整密钥只在创建时显示一次，请立即保存到密码管理器。

生成图片或视频会使用与 Pig AI 网页端相同的服务端积分账户。正式提交前可通过 `preview_billing` 查看预估扣费；最终扣费、退款和消费记录以服务端账本为准。

### 2. 下载或构建 MCP

预发布安装包可从 [`v0.1.0-beta.2`](https://github.com/B1gM8c/pig-ai-agent-kit/releases/tag/v0.1.0-beta.2) 下载，包含 macOS、Linux、Windows 的 amd64/arm64 构建、源码包和 [`checksums.txt`](https://github.com/B1gM8c/pig-ai-agent-kit/releases/download/v0.1.0-beta.2/checksums.txt)。下载后请先核对 SHA-256，再解压并在客户端配置中使用 `pig-ai-mcp`（Windows 为 `pig-ai-mcp.exe`）的绝对路径。

也可以使用 Go 1.25.14 或更高版本从源码构建：

```bash
git clone https://github.com/B1gM8c/pig-ai-agent-kit.git
cd pig-ai-agent-kit
mkdir -p bin
go build -o ./bin/pig-ai-mcp ./cmd/pig-ai-mcp
```

Windows PowerShell 请生成带 `.exe` 后缀的可执行文件：

```powershell
New-Item -ItemType Directory -Force .\bin | Out-Null
go build -o .\bin\pig-ai-mcp.exe .\cmd\pig-ai-mcp
```

`releases/latest` 只指向稳定版本，不会指向当前预发布版；首个稳定 Release 上线前请使用上面的固定版本链接。

### 3. 配置 API Key

macOS / Linux：

```bash
export PIGAI_API_KEY="<YOUR_PIGAI_API_KEY>"
```

Windows PowerShell：

```powershell
$env:PIGAI_API_KEY = "<YOUR_PIGAI_API_KEY>"
```

首选由宿主进程通过 `PIGAI_API_KEY` 环境变量提供 API Key。绝不要把真实 Key 写入仓库、共享配置、聊天内容、命令行参数或截图。公开 Base URL 为 `https://www.pigsec.cn/openapi/v1/ai`；`PIGAI_BASE_URL` 保持为 API origin `https://www.pigsec.cn`，MCP 会自动追加 `/openapi/v1/ai`。

环境变量：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `PIGAI_API_KEY` | 无 | 必填。Pig AI 开发者 API Key。 |
| `PIGAI_DOWNLOAD_DIR` | 用户缓存目录下的 `pig-ai-mcp/downloads` | 结果下载目录，必须是绝对路径。 |
| `PIGAI_HTTP_TIMEOUT` | `60s` | 单次 HTTP 请求超时，最大 `10m`。 |
| `PIGAI_MAX_DOWNLOAD_BYTES` | `1073741824` | 单个结果的最大下载字节数。 |
| `PIGAI_BASE_URL` | `https://www.pigsec.cn` | API origin；MCP 自动追加 `/openapi/v1/ai`。公开 Base URL 为 `https://www.pigsec.cn/openapi/v1/ai`。 |
| `PIGAI_ALLOW_CUSTOM_BASE_URL` | `false` | 自定义 `PIGAI_BASE_URL` 的显式开关。远程地址必须使用 HTTPS；HTTP 仅允许 loopback 测试地址。 |

### 4. 连接 MCP 客户端

所有客户端都应使用 MCP 二进制的**绝对路径**。

#### Codex

把 [examples/codex-config.toml](examples/codex-config.toml) 合并到 `~/.codex/config.toml`：

```toml
[mcp_servers.pig-ai]
command = "/ABSOLUTE/PATH/TO/pig-ai-mcp"
env_vars = ["PIGAI_API_KEY"]
```

`env_vars` 会转发启动 Codex 时已有的环境变量，不会把 Key 固化在配置文件中。重启 Codex 后可用 `/mcp` 检查连接。

#### Claude Desktop

Claude Desktop 的推荐分发形式是可将 API Key 标记为敏感字段的 `.mcpb` 包。**本项目目前尚未提供 `.mcpb` 产物**，请暂时使用 [examples/claude-desktop.json](examples/claude-desktop.json) 的手工配置作为 fallback，并把占位符替换为本机 Key：

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

该 fallback 会把 Key 以明文保存在本机 Claude 配置中，仅应在受保护的个人设备上使用。不要提交、共享、同步或备份此文件，限制其文件权限；建议创建仅授予实际需要的具体 scope 的独立 Key 并定期轮换。修改后完全退出并重新打开 Claude Desktop。

#### Cursor

把 [examples/cursor-mcp.json](examples/cursor-mcp.json) 保存到项目的 `.cursor/mcp.json`，或保存到 `~/.cursor/mcp.json` 供所有项目使用：

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

`${env:PIGAI_API_KEY}` 读取的是 Cursor 进程实际继承到的环境变量。配置后请从已设置该变量的环境启动 Cursor；仅在终端里临时 `export`，再从 Dock、开始菜单或其他未继承该环境的入口打开 Cursor，Key 不会自动可用。

### 5. 安装 Skill

直接下载 [`pig-ai-skill.zip`](https://github.com/B1gM8c/pig-ai-agent-kit/releases/download/v0.1.0-beta.2/pig-ai-skill.zip)，解压后得到可安装的 `pig-ai` 目录。也可以从已克隆的源码安装：

```bash
mkdir -p "$HOME/.agents/skills"
cp -R skills/pig-ai "$HOME/.agents/skills/"
```

Windows PowerShell：

```powershell
New-Item -ItemType Directory -Force "$HOME\.agents\skills" | Out-Null
Copy-Item -Recurse -Force "skills\pig-ai" "$HOME\.agents\skills"
```

预发布版同时提供固定文件名 `pig-ai-skill.zip` 和版本化归档 `pig-ai-skill_0.1.0-beta.2.zip`。首个稳定 Release 上线前，请使用上面的固定版本下载地址。

项目内共享时，将 `skills/pig-ai` 复制到仓库的 `.agents/skills/pig-ai`。Skill 变更通常会被自动发现；未出现时请重启客户端。

### 6. 验证

重启 MCP 客户端后验证连接。已安装 Skill 的 Codex 可发送：

```text
Use $pig-ai to call list_models. Do not create any image or video.
```

未安装 Skill 的纯 MCP 客户端则直接调用 `pig-ai` 服务的 `list_models` 工具；不要使用 `$pig-ai`，因为它是 Skill 名称，不是 MCP 工具名。

验证成功应返回当前可用的图片/视频模型能力。`list_models` 不创建生成任务。若返回 `401` 或 `403`，请检查 Key 是否有效以及是否包含 `ai:models` 权限。

## MCP 工具

| 工具 | 用途 |
| --- | --- |
| `list_models` | 查询已启用的模型、能力和可用参数。 |
| `preview_billing` | 预估积分、免费额度和余额影响。 |
| `create_image` | 幂等提交异步图片生成任务。 |
| `create_video` | 幂等提交异步视频生成任务。 |
| `get_operation` | 查询生成、耗时、结果和结算状态。 |
| `download_result` | 将一个结果安全下载到本地私有目录。 |
| `ensure_segments` | 幂等请求图片语义分层元数据。 |
| `get_segments` | 查询已持久化的分层状态和 bbox 数据。 |

详细参数和返回语义见 [Skill API 契约](skills/pig-ai/references/api.md)。公开 REST 文档可在 [Pig AI 开发者文档](https://www.pigsec.cn/ai/developer/docs) 查看。

## 安全与隐私

- MCP 只把请求发送到配置的 Pig AI API，不包含模型供应商密钥。
- API Key 只通过 `PIGAI_API_KEY` 提供，MCP 不会在工具输出中返回它。
- 结果下载使用私有目录、大小限制和 SHA-256 计算，并以不覆盖已有文件的方式原子发布。
- 发现漏洞请按 [SECURITY.md](SECURITY.md) 私下报告，不要在公开 Issue 中粘贴 Key、提示词或生成结果。

## 开发

```bash
go test ./...
go vet ./...
go build ./cmd/pig-ai-mcp
```

贡献前请阅读 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 许可证

[Apache License 2.0](LICENSE)
