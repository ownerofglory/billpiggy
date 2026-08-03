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
optimistic concurrency. The event and its outbox record are written in one database
transaction.

Projectors consume the outbox idempotently and write into context-owned schemas:
`expenses`, `budgets`, `analytics`, `reports`, `notifications`, and `audit`. A
checkpoint per projector means failed delivery can be retried safely. Projectors run
in-process initially; the outbox lets a future worker or message broker take over
without changing command handlers.

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
- MinIO is the initial S3-compatible object store for profile images, receipt
  originals, normalized receipt images, and generated reports. It is free, compact,
  and can later be replaced with any S3-compatible provider.
- Frequently read, low-churn data (role permissions, default categories, and user
  settings) is held in a bounded in-memory cache with explicit invalidation on its
  events. Redis remains optional; it is not justified for the initial user count.

Receipts are virus-scanned before extraction. The original is retained according to a
user-configurable retention policy; a normalized JPEG/PNG is resized and grayscale
converted before AI extraction to reduce storage and image-token cost.

## AI workloads

All AI features go through one outbound OpenAI adapter with per-user rate limits,
usage metrics, request IDs, and an explicit opt-in setting. The selected defaults are:

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
