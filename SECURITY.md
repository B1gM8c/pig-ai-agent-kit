# Security Policy

## Supported versions

Security fixes are applied to the latest tagged release. Update to the newest
release before reporting a problem that may already be fixed.

## Report a vulnerability

Use [GitHub private vulnerability reporting](https://github.com/B1gM8c/pig-ai-agent-kit/security/advisories/new).
Do not disclose a suspected vulnerability in a public issue or discussion.

Include only the information needed to reproduce and assess the issue:

- affected version, operating system, and architecture;
- expected and observed behavior;
- minimal reproduction steps or a small test fixture;
- security impact and any known mitigations.

Never include a real `PIGAI_API_KEY`, provider credential, personal data,
private prompt, or generated media. Replace secrets with obvious placeholders
and remove them from logs. If a Pig AI API key may have been exposed, revoke it
in the [developer center](https://www.pigsec.cn/ai/developer/keys) and create a
new one immediately.

Reports about credential leakage, unsafe redirects, path traversal, command or
argument injection, insecure file permissions, unbounded downloads, dependency
vulnerabilities, or MCP protocol handling are in scope for this repository.
The proprietary Pig AI service, its provider integrations, and account or
billing disputes are not implemented in this repository; use the Pig AI
website's support channel for those matters.

Maintainers will coordinate remediation and disclosure through the private
advisory. Please allow time for investigation before publishing details.
