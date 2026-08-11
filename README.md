# BillPiggy

A personal and household expense-tracking API: expenses, budgets, recurring payments,
analytics, an AI assistant, and invitation-only multi-user accounts with shared groups.
Go backend, PostgreSQL event store, hexagonal architecture.

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/ownerofglory/billpiggy)](go.mod)

## Features

- **Expense management** — manual entry, receipt photo/document scanning, free-text
  ("intelligent") entry, and audio dictation, all resolved through the same AI intake
  pipeline into a draft the user reviews before it's saved. Search, filter by category
  or tag, and share an expense with a user group.
- **Budgets** — per-category limits with configurable thresholds, due dates, and
  optional group sharing. Threshold crossings queue an alert email.
- **Scheduled (recurring) payments** — rent, insurance, subscriptions. Monthly,
  quarterly, yearly, or a custom day interval; auto-posts a confirmed expense as each
  occurrence falls due, with an optional advance-notice reminder.
- **Analytics** — spend rollups by day/week/month/year, category, and tag; budget
  suggestions and per-budget progress against each budget's own period; period-over-period
  comparison; top categories by change; burn rate (spend so far, projected total, budget
  target); daily totals and weekday breakdown for calendar-heatmap style views; largest
  individual expenses; periodically generated CSV/PDF reports.
- **AI assistant** — a scoped, tool-calling chat assistant that answers questions about
  the user's own expenses and budgets over a server-sent-events stream. Never has
  unrestricted database access.
- **Invitation-only accounts** — no self-registration. An administrator invites by
  email; the first super-admin bootstraps from configuration on first startup and is
  protected from being demoted or deleted. Roles are `member`, `admin`, `super_admin`.
- **User groups** — private sharing boundaries for shared expenses and budgets, visible
  to their creator, members, and super-admins.
- **Self-service password reset** — a one-time emailed token, structured to never let
  the request endpoint reveal whether an email has an account.
- **Profiles & preferences** — display name, email, password, profile image, and
  per-notification-kind email preferences.
- **Audit trail** — every domain event since account creation is queryable by a
  super-admin.

See [docs/architecture.md](docs/architecture.md) for how these are built: an
event-sourced core with synchronous command consistency and an outbox-driven
projection pipeline for cross-context propagation.

## Tech stack

- **Language:** Go 1.24
- **HTTP:** [chi](https://github.com/go-chi/chi) router
- **Database:** PostgreSQL, used as both the event store and read-model store
- **Object storage:** MinIO / any S3-compatible store (receipts, profile images,
  generated reports)
- **AI:** OpenAI-compatible chat completions via the official
  [`openai-go`](https://github.com/openai/openai-go) client, configurable base URL
- **Email:** [MailerSend](https://www.mailersend.com/) transactional API, rendered from Go
  templates, with a locally-enforced monthly send cap matching the provider's quota
- **Deployment:** Docker image, Helm chart, Terraform for the initial cluster
  infrastructure — see [docs/production-deployment.md](docs/production-deployment.md)
- **API docs:** OpenAPI 2.0, generated from handler annotations into
  [api/openapi.yaml](api/openapi.yaml)

## Getting started

### Prerequisites

- Go 1.24+
- Docker (for local PostgreSQL/MinIO)

### Run locally

```sh
cp .env.example .env    # fill in JWT_SECRET, BOOTSTRAP_SUPER_ADMIN_EMAIL/PASSWORD, etc.
docker compose up --build postgres minio
make run
```

The server listens on `:8080` by default. Every API route is under
`/billpiggy/api/v1`; health probes are at `/livez`, `/readyz`, and `/startupz`; metrics
are exposed at `/metrics`.

Log in with the bootstrap super-admin credentials from `.env` to get started — that
account only self-creates when no super-admin exists yet, so keep those credentials
somewhere safe.

Postgres applies tracked migrations automatically on first startup. To recreate the
database from migrations, stop Compose and remove the `billpiggy-postgres-data` volume
before starting it again.

### Common tasks

```sh
make test              # unit tests (in-memory adapters, no Docker required)
make test-integration  # + adapter tests against real Postgres/MinIO (needs `docker compose up -d`)
make coverage           # unit tests with coverage and the race detector
make check             # fmt + vet + test + build + helm-lint + helm-template
make generate-openapi  # regenerate api/openapi.yaml from handler annotations
```

Run `make help` for the full target list.

## API documentation

The OpenAPI spec lives at [api/openapi.yaml](api/openapi.yaml) and is regenerated from
`@Summary`/`@Router`/etc. annotations on each handler via `make generate-openapi`. CI
fails a pull request if the committed spec has drifted from the handlers.

## Deployment

BillPiggy ships as a Docker image plus a Helm chart ([charts/billpiggy](charts/billpiggy)),
deployed to Kubernetes through GitHub Actions workflows — never applied from a local
machine. See:

- [docs/production-deployment.md](docs/production-deployment.md) — required GitHub
  Environment secrets/variables, infrastructure provisioning, and the deploy workflow
- [docs/backup-and-disaster-recovery.md](docs/backup-and-disaster-recovery.md) —
  automated PostgreSQL/MinIO backups, restore steps, and current DR limitations
- [infra/terraform](infra/terraform) — Terraform root that provisions the initial
  PostgreSQL and MinIO releases

## Project structure

```
cmd/billpiggy/             application entry point and wiring
internal/core/domain/      aggregates, events, and domain policies
internal/core/service/     application services (command handlers, projections)
internal/core/port/        inbound and outbound interfaces
internal/adapter/inbound/  HTTP handlers (chi router, v1 API)
internal/adapter/outbound/ PostgreSQL, MinIO, OpenAI, in-memory, and cached adapters
pkg/                       reusable cross-cutting packages (auth, outbox, ratelimit, ...)
migrations/                versioned SQL migrations
charts/billpiggy/          Helm chart
infra/terraform/           infrastructure-as-code for the initial cluster dependencies
docs/                      architecture and operations documentation
```

## Contributing

Pull requests run formatting, `go vet`, the unit test suite, an OpenAPI-drift check,
and Postgres/MinIO-backed integration tests — see
[.github/workflows/pull-request-checks.yaml](.github/workflows/pull-request-checks.yaml).
Run `make check` locally before opening a PR.

## License

[MIT](LICENSE)
