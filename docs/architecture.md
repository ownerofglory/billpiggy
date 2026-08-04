# BillPiggy architecture

## Runtime shape

BillPiggy is a modular monolith. It has one deployable Go service and one PostgreSQL
instance. The service is organized by hexagonal boundaries:

- inbound adapters expose HTTP APIs, scheduled jobs, and future AI/file callbacks;
- application services validate commands and append domain events;
- domain packages define aggregates, events, and policies;
- outbound adapters implement PostgreSQL, S3-compatible object storage, email, and
  OpenAI clients.

Bounded contexts are `identity`, `expenses`, `budgets`, `analytics`,
`notifications`, `reports`, and `assistant`. Contexts do not query one another's
write models. They consume events and maintain their own projection tables.

## Event store and projections

PostgreSQL is the initial event store. This is deliberately simpler than operating a
separate event database for a family-sized deployment, while preserving the
event-driven boundaries needed to scale later.

`events.events` is an append-only event stream. Every event has an aggregate type,
aggregate ID, aggregate version, payload, metadata, and correlation/causation IDs.
The `(aggregate_type, aggregate_id, aggregate_version)` unique constraint implements
optimistic concurrency. `global_seq` gives the store a single monotonic order that the
delivery queue drains by; `aggregate_version` orders events within one aggregate.

Two kinds of consistency are kept deliberately separate.

**Command consistency is synchronous.** An aggregate's own read model
(`expenses.expenses`, `budgets.budgets`) commits in the same transaction as its event.
Services obtain that transaction from the `UnitOfWork` port; `pkg/pgxtx` propagates the
`pgx.Tx` through the context so every outbound adapter joins it without any port
changing shape, and the in-memory adapters get equivalent snapshot-and-restore
semantics so the behaviour is testable without a database. Services always append the
event first, which takes the aggregate advisory lock before any row lock and gives
concurrent commands on one aggregate a consistent lock order.

**Cross-context propagation is asynchronous.** `events.outbox` fans each event out to
one row per registered subscription in `events.subscriptions`, so adding a projection
is a row rather than a schema change. `pkg/outbox` drives them: one transaction per
message, never per batch, with leases, exponential backoff through `available_at`,
dead-lettering after a bounded number of attempts, and `last_error` recorded for
operators. Handlers run inside the store's transaction, so a projection write, the
outbox acknowledgement and anything else the handler queues — a budget-alert email,
for instance — all commit together or not at all.

Ordering rests on a per-aggregate blocker guard rather than a global lock: a message
waits while any earlier message for the *same* aggregate is still pending or
dead-lettered, and messages for unrelated aggregates flow past it freely. Global order
across unrelated aggregates is irrelevant to every projection here; same-aggregate
order is what the reversal logic depends on. A poison event therefore freezes one
aggregate's projection instead of corrupting it or stalling the queue.

Backfill is not a separate code path. Registering a subscription that has never run
enqueues the existing history with `replay` set, so it drains through the same engine
and handler as live traffic while handlers suppress user-visible side effects.
`events.projector_checkpoints` records progress per subscription for operator
visibility and lag reporting; it is explicitly **not** a skip watermark, because
sequence values are assigned before commit and gaps are transiently visible. The
outbox row status is the only authority on what remains undone.

The current subscriptions are `analytics_rollups` (category and tag rollups),
`budget_usage` (per-period spend plus threshold alerts), and `audit_trail` (every
aggregate type). Projections live in `internal/core/service` and depend only on
outbound ports, so one implementation serves both PostgreSQL and the in-memory
adapters. Each engine exposes a readiness check and runs in-process; the outbox lets a
future worker or message broker take over without changing command handlers.

TimescaleDB is intentionally not a runtime dependency for the first deployment.
The event stream is indexed by aggregate and timestamp, and analytics projections are
pre-aggregated. If event volume makes time-window scans material, the `events.events`
table can be converted to a TimescaleDB hypertable in a forward migration.

## Identity and authorization

Users cannot self-register. An administrator creates an invitation, and only a
successful invitation redemption creates a user. The first startup requires
`BOOTSTRAP_SUPER_ADMIN_EMAIL` and `BOOTSTRAP_SUPER_ADMIN_PASSWORD`; the app fails
closed when neither a super-admin nor bootstrap credentials are present.

The authorization package follows the structure of the referenced
[`production-saas-starter` auth module](https://github.com/moasq/production-saas-starter/tree/main/go-b2b-starter/internal/modules/auth): identity is put in request context and permissions are modeled as `resource:action`. It is implemented locally with signed JWT access tokens, hashed and rotated refresh tokens, and application-owned roles rather than the reference project's Stytch adapter.

Roles are `member`, `admin`, and `super_admin`. Super-admin mutation guards are domain
rules: no actor can delete the super-admin or remove its permissions. User groups are
private to their creator and members, except that super-admins can inspect all groups.

## Data and file storage

- PostgreSQL stores events, projections, invitations, refresh-token hashes, audit
  records, and report metadata.
- MinIO is the S3-compatible object store for profile images, receipt images, and
  generated reports. It is free, compact, and can later be replaced with any
  S3-compatible provider. Production Terraform provisions a scoped application user
  through the chart's own provisioning job, limited by policy to the `billpiggy`
  bucket; the root credentials it also creates are operator-only and the application
  never sees them.
- Frequently read, low-churn data (role permissions, default categories, and user
  settings) is a candidate for a bounded in-memory cache with explicit invalidation on
  its events, but that cache does not exist yet. Redis remains optional regardless; it
  is not justified for the initial user count.

Uploads are sniffed by content rather than trusted by declared `Content-Type`, and
images are re-encoded rather than stored as received: re-encoding is itself the
metadata strip, since the output is rebuilt from decoded pixels and cannot carry EXIF
GPS data or an appended payload through. Receipts are additionally downscaled and
converted to grayscale before storage, both to save space and because a grayscale,
bounded-size image is what the OCR extraction workload should be spending tokens on;
profile images are downscaled only. PDFs bypass normalization and are stored as
received. Malware scanning is not implemented — a receipt upload from a compromised
account is not currently inspected before storage.

Replacing or deleting a resource does not delete its object synchronously: object
storage cannot join the database transaction that stopped referencing it. Instead the
transaction marks the object orphaned in `files.object_references`, and a background
sweeper deletes it from MinIO and forgets the reference afterward. A crash between the
two leaves an orphaned row for the next sweep to retry, which is a deliberate,
low-cost trade for a single-node deployment rather than the coordination a distributed
two-phase delete would need. There is no retention policy beyond "delete when
replaced or when the owning resource is deleted" — no user-configurable retention
window exists yet.

## AI workloads

All AI features go through one outbound adapter behind the `AIProvider` port, with
per-user rate limits, usage metrics, request IDs, and an explicit opt-in setting.

The provider is abstracted in the domain rather than at the HTTP level. `domain.Message`,
`domain.Tool`, `domain.ToolCall`, `domain.Completion` and `domain.CompletionRequest`
describe a model conversation in application terms, and no OpenAI type crosses into the
core. The port is two methods — `Complete` and `Stream` — because tools, structured
output and model choice all travel inside the request rather than multiplying methods;
adding a capability then does not change the interface every adapter and fake implements.

The adapter wraps the official [`openai-go`](https://github.com/openai/openai-go) client
rather than hand-rolled HTTP calls, so request shaping, retries and SSE framing are the
library's concern. Its base URL is configurable through `OPENAI_BASE_URL`, which lets the
adapter be driven by an `httptest` server in tests and routed through a compatible
gateway in a deployment.

Streaming is genuine end to end: the adapter forwards each token delta as the provider
emits it, and the SSE handler relays it immediately. Tool calls are the exception — their
arguments arrive split across chunks and are meaningless until complete, so they are
accumulated by index and delivered once, whole, on the final chunk.

The selected model defaults are:

| Workload | Default model | Why |
| --- | --- | --- |
| Receipt image/document extraction and sentence-to-expense parsing | `gpt-4o-mini` | Low-cost image input and structured-output support. |
| Expense and budget assistant | `gpt-5.6-luna` | Cost-sensitive, high-volume tool-enabled reasoning. |
| Uploaded-audio transcription | `gpt-4o-mini-transcribe` | Low-cost batch transcription. |
| Live dictation, when enabled | `gpt-realtime-whisper` | Streaming speech-to-text. |

The assistant only receives data obtained through scoped application tools (for
example, `list_expenses` for the authenticated user). It never receives unrestricted
database access. Start with a per-user limit of 10 assistant requests/minute and 30
receipt extractions/day; limits are configuration values and administrators can lower
them.

The authenticated assistant chat endpoint is an SSE stream. `POST
/billpiggy/api/v1/assistant/chat` responds with `text/event-stream` and emits
`message.started`, `message.delta`, `message.citation`, `message.completed`, and
`message.error` events. The handler flushes each event, stops OpenAI work when the
client disconnects, and never includes a token, database error, or internal tool
payload in an error event.

OpenAI's current model catalog describes GPT-5.6 Luna as optimized for cost-sensitive
workloads and GPT-4o mini as a low-cost image-capable model. See the [model catalog](https://developers.openai.com/api/docs/models) and [GPT-4o mini reference](https://developers.openai.com/api/docs/models/gpt-4o-mini).

## Operations

The service writes structured JSON via `log/slog`. It exposes:

- `/livez`: process is alive;
- `/readyz`: PostgreSQL, event projector, and required object-storage dependencies
  are ready;
- `/startupz`: migrations, bootstrap, and required components completed startup;
- `/metrics`: Prometheus process, HTTP, event-processing, AI usage, invitation, and
  business metrics.

Email notification delivery and report generation are asynchronous outbox consumers.
All identity/admin changes and permission-sensitive actions produce immutable audit
records.
