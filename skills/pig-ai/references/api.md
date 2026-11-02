# Pig AI local MCP tool contract

The Skill performs actions through the local `pig-ai` stdio MCP server. The
REST mappings below document the public boundary but are not instructions to
bypass MCP. The MCP client authenticates with `PIGAI_API_KEY`, sends
`X-API-Key` to the public API, unwraps successful `{code, data, message}`
responses, and redacts credentials from errors. It declares input schemas but
does not impose an independent output schema: successful tool content is the
server's current `data` JSON, so do not invent fields missing from a response.

The production API is available at the public Base URL
`https://www.pigsec.cn/openapi/v1/ai`. The public OpenAPI specification exposes
the eight `/openapi/v1/ai/*` paths listed below, all authenticated with
`X-API-Key`. The MCP client's `PIGAI_BASE_URL` is the API origin
`https://www.pigsec.cn`; the client appends `/openapi/v1/ai` automatically, so
it should normally remain unset. Any custom base URL, including a loopback
fixture, requires `PIGAI_ALLOW_CUSTOM_BASE_URL=true`. Remote custom origins
must use HTTPS; plain HTTP is accepted only for loopback test fixtures.

## Tool index and scopes

| MCP tool | Public REST mapping | Required scope |
| --- | --- | --- |
| `list_models` | `GET /openapi/v1/ai/models` | `ai:models` |
| `preview_billing` | `POST /openapi/v1/ai/billing-preview` | `ai:billing` |
| `create_image` | `POST /openapi/v1/ai/images` | `ai:images` |
| `create_video` | `POST /openapi/v1/ai/videos` | `ai:videos` |
| `get_operation` | `GET /openapi/v1/ai/operations/{operation_id}` | `ai:operations` |
| `download_result` | `GET /openapi/v1/ai/operations/{operation_id}/results/{result_id}/content` | `ai:results` |
| `ensure_segments` | `POST /openapi/v1/ai/operations/{operation_id}/results/{result_id}/segments/ensure` | `ai:segments` |
| `get_segments` | `GET /openapi/v1/ai/operations/{operation_id}/results/{result_id}/segments` | `ai:segments` |

A key with `ai:*` grants all public AI scopes. Key creation, listing,
revocation, usage records, and administrator routes use the signed-in Pig AI
website and are intentionally outside this MCP contract.

## 1. `list_models`

Use this tool before selecting a model or any model-dependent control.

Input:

```json
{}
```

Output contains `models`, `video_models`, and their current provider/model
capability metadata. Treat it as the source of truth for accepted modes,
sizes, quality values, image count, duration, aspect ratio, resolution, and
first-frame support. Do not hard-code a provider catalog in an agent workflow.

## 2. `preview_billing`

Preview the server-side points and free allowance for one prospective request.
Pass the same model-dependent fields that will be used for generation.

Input fields:

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `provider_id` | string | no | Enabled provider from `list_models`. |
| `model` | string | no | Enabled model from `list_models`. |
| `mode` | string | yes | `text_to_image`, `image_to_image`, `text_to_video`, `image_to_video`, or `reference_to_video`. |
| `size` | string | no | Image size supported by the selected model. |
| `quality` | string | no | Image quality supported by the selected model. |
| `n` | integer | yes | `1`–`4`; video modes require `1`. |
| `workflow` | string | no | Exact enabled workflow from the current capability response. |
| `resolution`, `aspect_ratio` | string | no | Exact generation specification. |
| `width`, `height`, `duration` | integer | no | Non-negative explicit dimensions or video duration. |
| `reference_image_count` | integer | no | Accurate image count, at most 30; model/create limits may be narrower. |
| `reference_video_count`, `reference_audio_count` | integer | no | Accurate count, at most 10 each. |
| `transitions` | array | no | At most 19 `{prompt, duration}` objects; duration is a positive number of seconds. |

Example:

```json
{
  "mode": "text_to_image",
  "size": "1024x1024",
  "quality": "standard",
  "n": 1
}
```

Output may include `points_cost`, `charge_points`, `free_usage_cost`,
`free_available`, `free_used`, `free_remaining`, `daily_free_limit`,
`points_balance`, and `vip_level`. It is advisory; a later terminal operation
contains the authoritative settlement projection. Retail-enabled Dreamina/Seedance
models require the returned `quote_id` with the same model/provider/workflow,
resolution/duration/aspect ratio, transitions and reference counts when creating.
An unpriced specification is unavailable, not free. `409 quote_stale` requires a
fresh preview and price confirmation, without replacing an accepted operation.

## Shared media reference

`create_image` and `create_video` accept media-reference objects:

| Field | Type | Notes |
| --- | --- | --- |
| `role` | string | Stable semantic role such as `reference`, `style`, `mask`, or `first_frame`; the local MCP client defensively limits it to 80 characters. |
| `source_type` | string | `url` or `file`; may be omitted when the source makes it unambiguous. |
| `url` | string | Absolute HTTPS URL; the local MCP client defensively limits it to 4,096 characters. HTTP is accepted only for loopback test fixtures, not production references. |
| `content_base64` | string | Standard base64 file content for `source_type: file`. |
| `slot` | integer | Optional stable zero-based slot `0`–`3` for image creation, `0`–`6` for video `reference_images`. Keep it unchanged across retries. |
| `name` | string | Optional local filename metadata for inline content. |
| `content_type` | string | Optional valid MIME type used to choose a default filename extension for inline content; the server identifies the uploaded bytes independently. |

Set exactly one of `url` and `content_base64`. Image references are ordered,
limited to four, and inline bytes are limited to 12 MiB per reference. An
inline mask is limited to 4 MiB. The complete MCP JSON argument frame is
limited to 32 MiB, including base64 expansion and all references; use HTTPS
URLs instead of combining several large inline files. The MCP client constructs
the exact multipart manifest; callers do not construct multipart field names
themselves. If a slot is omitted, the client assigns that reference's array
index. An explicitly provided slot, including `0`, is preserved. Slots must be
unique.

## 3. `create_image`

Submit one asynchronous image operation.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `prompt` | string | yes | UTF-8 prompt, 2–10,000 Unicode characters after trimming and within the selected model's current limit. |
| `idempotency_key` | string | yes | Stable 8–128 character key for this logical request. |
| `provider_id` | string | no | Enabled provider from `list_models`. |
| `model` | string | no | Enabled image model from `list_models`. |
| `mode` | string | yes | `text_to_image` or `image_to_image`. |
| `size` | string | no | Selected model's supported size. |
| `quality` | string | no | Selected model's supported quality. |
| `n` | integer | yes | `1`–`4`, within the model limit. |
| `references` | array | no | Ordered media references. |
| `mask` | object | no | One media reference used as a mask. |
| `quote_id` | string | model-dependent | Quote returned by `preview_billing`. |
| `workflow`, `resolution` | string | no | Enabled Dreamina workflow and exact quoted resolution. |
| `width`, `height` | integer | no | Non-negative custom image dimensions supported by the model. |

`text_to_image` must not include `references` or `mask`.
`image_to_image` requires at least one item in `references`; a mask alone does
not satisfy that requirement.

Example:

```json
{
  "prompt": "a red paper kite above a quiet lake",
  "idempotency_key": "7de71bbf-5a1a-47a4-b711-6b0e4c522b2d",
  "mode": "text_to_image",
  "size": "1024x1024",
  "quality": "standard",
  "n": 1
}
```

For image-to-image generation, preserve reference order and slots:

```json
{
  "prompt": "keep the composition and render it as paper cut art",
  "idempotency_key": "c7b290af-8e8f-48db-a706-f74ba1293a37",
  "mode": "image_to_image",
  "n": 1,
  "references": [
    {
      "role": "style",
      "source_type": "url",
      "url": "https://cdn.example/reference.png",
      "slot": 0
    }
  ]
}
```

## 4. `create_video`

Submit one asynchronous video operation.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `prompt` | string | yes | UTF-8 prompt, 2–10,000 Unicode characters after trimming and within the selected model's current limit. |
| `idempotency_key` | string | yes | Stable 8–128 character key for this logical request. |
| `provider_id` | string | no | Enabled provider from `list_models`. |
| `model` | string | no | Enabled video model from `list_models`. |
| `aspect_ratio` | string | no | Value supported by the selected model. |
| `resolution` | string | no | Value supported by the selected model. |
| `duration` | integer | no | `0` or omitted selects the model default; otherwise use a positive integer supported by the selected model in `list_models`. |
| `first_frame`, `last_frame` | object | no | Ordered first/last image manifests if supported. |
| `quote_id`, `workflow` | string | model-dependent | Exact quote and enabled workflow. |
| `reference_images` | array | no | Up to seven ordered image manifests, slots 0–6. |
| `keyframes` | array | no | Up to four `{image, timestamp_s}` objects; times strictly increasing, within duration, on a one-third-second grid. |
| `reference_audios` | array | no | Up to three preset `{voice_id}` objects; order maps to AUDIO_0…AUDIO_2. |
| `reference_video_urls`, `reference_audio_urls` | array | no | Ordered HTTPS URLs of uploaded media, at most 10 each; distinct from preset voices. |
| `transitions` | array | no | At most 19 `{prompt, duration}` objects with a positive duration. |

Example:

```json
{
  "prompt": "a slow camera move over a mountain lake at sunrise",
  "idempotency_key": "8b786013-e25d-4d2b-ae5b-260612c694ba",
  "aspect_ratio": "16:9",
  "resolution": "720p",
  "duration": 5
}
```

Video has no `mode`, `n`, `references`, `mask`, `size`, or `quality` fields.
Use only media forms supported by the selected model/workflow. First/last frames,
reference images and keyframes together are limited to nine image inputs. Inline
image content becomes multipart parts; audio/video inputs use HTTPS URLs already
uploaded through the application. This MCP adapter has no direct audio/video
binary upload tool. Do not silently discard unsupported references.

### Creation output and idempotency

Both creation tools return an unwrapped object containing `operation`, usually
with initial state such as:

```json
{
  "operation": {
    "id": "op_...",
    "kind": "image",
    "status": "accepted",
    "generation_status": "pending",
    "settlement_status": "pending",
    "requested": 1,
    "completed": 0,
    "charge_points": 0,
    "refund_points": 0,
    "net_points": 0,
    "results": []
  }
}
```

The server guarantees that replaying the same idempotency key with the same
request returns the original operation without a second charge; the local MCP
client only forwards the key. Reusing it with different request content
returns `409 idempotency_conflict`; do not retry that changed request under the
same key.

## 5. `get_operation`

Input:

```json
{"operation_id": "op_..."}
```

The identifier is 1–64 characters and may contain letters, digits, underscore,
dot, colon, or hyphen in any position. Output contains
`operation` with:

- `status`: `accepted`, `processing`, `succeeded`, or `failed`;
- `generation_status`: `pending`, `running`, `succeeded`, or `failed`, plus
  `settlement_status`;
- `requested`, `completed`, `charge_points`, `refund_points`, and
  `net_points`;
- accepted, queue, and generation latency fields when available;
- zero or more result objects with stable result IDs and media metadata;
- optional `provider_progress` with `provider_status`, `queue_position`,
  `queue_total`, `estimated_remaining_seconds`, `observed_at_ms`, `stage`, and
  optional `progress_percent`/`progress_source` (`provider` or `estimated`);
- optional structured `error: {code,message}` and compatibility fields
  `progress`/`progress_text`.

The client preserves server values. Label estimated percentages as estimates;
retain observation timestamps and mark stale values. Queue counts are tasks, not
people. Missing percentage is indeterminate, not 0% or 100%; legacy `progress`
must not be labeled as an upstream rendering percentage. An uncertain operation
is not a failed submission: query the original ID rather than generating again.
Stop a bounded local wait without canceling the job, and resume it later rather
than imposing a 24-hour tracking cutoff.

Poll with bounded backoff until `status` is `succeeded` or `failed` and
`settlement_status` is `settled`, `refunded`, or `not_charged`.
`pending`, `reserved`, and `refund_pending` are non-terminal settlement
states. A succeeded operation may have `completed < requested`; this is
partial success, and its returned result IDs remain downloadable. A failed
operation does not promise results. Do not infer a refund from transport
status.

## 6. `download_result`

Input:

```json
{
  "operation_id": "op_...",
  "result_id": "result_..."
}
```

Use only an operation/result pair returned by `get_operation`. The MCP client
streams the content to its configured private directory, enforces the maximum
size, computes SHA-256, and atomically publishes it without overwriting an
existing file. Output is local metadata, not media bytes:

```json
{
  "path": "/private/download/directory/result.png",
  "mime_type": "image/png",
  "bytes": 123456,
  "sha256": "...",
  "operation_id": "op_...",
  "result_id": "result_..."
}
```

## 7. `ensure_segments`

Input:

```json
{
  "operation_id": "op_...",
  "result_id": "result_..."
}
```

Use this only for an image result. It starts or resumes the persisted semantic
segmentation for the same operation/result pair. Repeating it is naturally
idempotent and does not create another billable generation operation.

## 8. `get_segments`

Input:

```json
{
  "operation_id": "op_...",
  "result_id": "result_..."
}
```

Output contains the persisted segmentation status and, when complete,
schema-versioned image/group/region metadata. If status is `not_started`, call
`ensure_segments`; do not poll an unchanged `not_started` record. Poll while
status is `pending` or `processing`; `completed` and `failed` are terminal. The
contract exposes bbox/region metadata only; it does not promise a pixel mask,
layered design file, or browser Canvas equivalence.

## Errors and retry behavior

MCP errors have the normalized shape:

```json
{
  "code": "rate_limited",
  "message": "request rate limit exceeded",
  "http_status": 429,
  "retry_after": "2",
  "details": {}
}
```

- Retry transport failures and explicitly retryable statuses only.
- Honor `retry_after` and keep retries bounded.
- Always reuse the original idempotency key for a logical creation retry.
- `401`, `403`, validation failures, insufficient points, and idempotency
  conflicts require account, configuration, or request changes.
- A `404` for an operation/result may intentionally hide a resource not owned
  by the current key owner.
