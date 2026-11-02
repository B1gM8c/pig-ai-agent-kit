# Changelog

All notable changes to Pig AI Agent Kit will be documented in this file.

The project follows [Semantic Versioning](https://semver.org/).

## Unreleased

## [0.1.0-beta.3] - 2026-11-02

### Added

- Dreamina and Seedance quote-to-create parameters, first/last frames, ordered
  keyframes, image references, audio/video reference URLs, and preset voices.
- Conditional prompt validation for multiframe video workflows.
- Documented provider progress, queue/ETA, structured errors, and safe original
  operation tracking without automatic generation retries.

### Changed

- Build and check releases with patched Go 1.26.10. Existing origin, API-key,
  download, and unknown-argument safeguards remain in place.

## [0.1.0-beta.2] - 2026-09-19

### Changed

- Document the production Pig AI API Base URL, `X-API-Key` authentication, and
  environment-variable-only API key handling now that the public API is live.
- Align the README and Skill API reference with the eight public OpenAPI paths.

## [0.1.0-beta.1] - 2026-09-18

### Added

- Public Pig AI MCP server for image and video generation workflows.
- Pig AI Skill with API contract and agent metadata.
- Cross-platform release archives, Skill ZIP, source archive, and SHA-256 checksums.
