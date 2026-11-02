---
name: pig-ai
description: Use the local Pig AI MCP tools to create or retrieve Pig AI images and videos, preview point charges, monitor asynchronous operations, download results, or request image segmentation metadata.
license: Apache-2.0
metadata:
  compatibility: Requires a configured local pig-ai stdio MCP server and PIGAI_API_KEY.
---

# Pig AI

Use the configured local `pig-ai` MCP server for every action. Do not call the
REST API directly, recreate the HTTP client, or infer provider-specific
behavior. If the MCP server or a required tool is unavailable, stop and tell
the user to configure `pig-ai-mcp` and `PIGAI_API_KEY`.

## Credentials and boundaries

- The MCP process reads `PIGAI_API_KEY` from its environment. Never ask the
  user to paste a key into chat or include a key in a prompt, tool argument,
  command line, log, or generated file.
- Provider routing, generation, point charging, refunds, audit records, and
  result storage are server-side. Do not claim that the Skill or MCP performs
  or verifies those functions independently.
- Do not submit a billable generation when the user only asks about models,
  pricing, documentation, or setup.

## Workflow

1. Call `list_models` when the model, capability, valid controls, or defaults
   are unknown. Use only values returned by the current capability response.
2. Call `preview_billing` when the user asks about cost or when showing the
   expected point charge before generation is useful. For retail-enabled
   Dreamina/Seedance, preview is required: retain the returned `quote_id` and
   send it with the identical specification when creating. Always provide its
   required `mode` and `n`. Treat it as an estimate; the terminal server ledger
   is authoritative.
3. For each logical generation, create one stable 8–128 character
   `idempotency_key`. Reuse it on transport retries and after timeouts. Never
   create a new key merely because the first response was lost.
4. Call `create_image` or `create_video` only after the user has requested the
   corresponding billable action. For `create_image`, always provide `mode`
   and `n`; `text_to_image` accepts no references or mask, while
   `image_to_image` requires at least one reference. Preserve ordered image
   references and their slots. These tools return an asynchronous operation,
   not final media.
5. Poll `get_operation` with bounded backoff until operation `status` is
   `succeeded` or `failed` and `settlement_status` is `settled`, `refunded`, or
   `not_charged`, or until the caller's deadline expires. `pending`,
   `reserved`, and `refund_pending` are non-terminal settlement states. Honor
   `retry_after` on rate limits.
6. Report `requested`, `completed`, `charge_points`, `refund_points`, and
   `net_points` from the terminal operation. A succeeded operation with fewer
   completed than requested is a partial success; expose its returned results
   and refund fields rather than relabeling it as total failure.
7. Call `download_result` only with result IDs returned by that operation.
   Return the local path, MIME type, byte count, and SHA-256. Do not expose
   binary data or invent an upstream URL.
8. For an image whose semantic regions are requested, call `ensure_segments`
   once and poll `get_segments` until its persisted status is `completed` or
   `failed`; `pending` and `processing` are non-terminal. If a read returns
   `not_started`, call `ensure_segments` rather than polling that unchanged
   state. Segment output is bbox/region metadata, not a pixel mask, PSD, or
   editable-layer equivalent.

## Dreamina / Seedance media and progress

Use the exact enabled identity and `workflow` returned by `list_models`.
Image creation supports `resolution`, `width`, and `height`; video creation
supports `aspect_ratio`, `resolution`, `duration`, and `transitions` but has no
`mode` field. Billing preview still uses `mode` to select the quote family.
Reference images use the ordered manifest. Dreamina reference videos and
uploaded audio use `reference_video_urls` and `reference_audio_urls` (HTTPS
URLs of uploaded media); `reference_audios: [{voice_id}]` is the distinct preset
voice contract. Preserve ordering and never silently discard unsupported media.
The MCP adapter does not implement direct audio/video file upload.

Return `provider_progress`, legacy `progress`/`progress_text`, and structured
`error` from the operation
without inventing values. A service progress value can be a stage or estimate;
it is not a promise of the provider's rendering percentage. Missing progress
means indeterminate, not zero or completion. Prefer `provider_progress` when
available: label `progress_source: estimated` as estimated, preserve
`observed_at_ms`, and display queue/ETA fields only when supplied. Legacy
`progress` must not be labeled as an upstream percentage. `generation_uncertain` / `50131`
means query the original operation, not start another paid generation.

## Errors and recovery

Treat tool errors as structured `code`, `message`, `http_status`,
`retry_after`, and `details` data. Retry only transport failures and explicitly
retryable statuses, always with the original idempotency key. Authentication,
scope, validation, insufficient-points, and idempotency-conflict errors require
configuration or user action.

`409 quote_stale` requires a fresh preview and price confirmation before a new
accepted operation exists; never replace an accepted operation to refresh its
quote. A specification without a configured price is unavailable, not free.

A timeout does not prove that submission failed. Continue with a previously
returned operation ID; otherwise retry the same logical submission with the
same idempotency key. Stop a bounded local wait without canceling the operation;
retain its ID for later reads, including after 24 hours. Never infer a charge or refund from an HTTP status alone.

Read [references/api.md](references/api.md) when exact tool arguments, public
endpoint mappings, media-reference rules, scopes, or output semantics are
needed.
