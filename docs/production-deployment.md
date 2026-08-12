# Production deployment

BillPiggy deploys to Kubernetes through the manual **Deploy Kubernetes production**
workflow. The workflow installs a published OCI Helm chart and deploys the selected
container image; it does not build images, push charts, or apply database migrations.

Create a GitHub Environment named `production` and attach the deployment secrets and
variables below to that environment. Environment protection rules are recommended so
that production deployment requires approval.

## GitHub Environment secrets

| Secret | Required | Purpose |
| --- | --- | --- |
| `KUBERNETES_CA_DATA` | Yes | Base64-encoded Kubernetes cluster CA certificate. |
| `KUBERNETES_CLUSTER_SERVER_URL` | Yes | Kubernetes API server URL. |
| `KUBERNETES_NAMESPACE` | Yes | Namespace receiving the release, for example `billpiggy`. |
| `KUBERNETES_CLIENT_CA_DATA` | Yes | Base64-encoded X.509 client certificate used by GitHub Actions. |
| `KUBERNETES_CLIENT_KEY_DATA` | Yes | Base64-encoded private key matching the client certificate. |
| `DATABASE_URL` | Yes | Production PostgreSQL connection string. |
| `JWT_SECRET` | Yes | Random application signing secret with at least 32 bytes. |
| `BOOTSTRAP_SUPER_ADMIN_EMAIL` | First deploy | Email address for the initial super-admin. |
| `BOOTSTRAP_SUPER_ADMIN_PASSWORD` | First deploy | Strong password for the initial super-admin. |
| `OPENAI_API_KEY` | Optional | Reserved for OpenAI-backed application capabilities. |
| `METRICS_TOKEN` | Optional | Static bearer token gating `/metrics` (Prometheus can't do the interactive JWT login every other endpoint expects). Empty leaves `/metrics` unreachable rather than open — set this, and configure your scraper to send it as `Authorization: Bearer <token>`, before relying on scraping. Not yet wired into the chart's Secret template; there is no in-cluster `ServiceMonitor` today, so this has no consumer until one exists. |
| `MAILERSEND_API_KEY` | Optional | MailerSend API token. Enables the notification/invitation email worker when set; leave unset to run without outgoing email. |
| `MAILERSEND_FROM_EMAIL` | Optional | Sender address for BillPiggy notification email. |
| `MAILERSEND_FROM_NAME` | Optional | Sender display name. |
| `MINIO_ENDPOINT` | Yes | In-cluster MinIO endpoint, e.g. `billpiggy-minio.billpiggy-infra.svc.cluster.local:9000`. |
| `MINIO_ACCESS_KEY` | Yes | The scoped application user's access key (`MINIO_APP_USER` below) — never the root user. |
| `MINIO_SECRET_KEY` | Yes | The scoped application user's secret key (`MINIO_APP_PASSWORD` below). |
| `DOCKER_USER` | Image workflows | Docker Hub user used as the default image namespace. |
| `DOCKER_TOKEN` | Image workflows | Docker Hub access token. |
| `POSTGRES_PASSWORD` | IaC workflow | Password for the PostgreSQL application and administrative users. |
| `MINIO_ROOT_USER` | IaC workflow | MinIO root access key. Operator-only; the application never uses it. |
| `MINIO_ROOT_PASSWORD` | IaC workflow | MinIO root secret key. |
| `MINIO_APP_USER` | IaC workflow | Access key for the application's scoped MinIO user, provisioned by Terraform with access to only the `billpiggy` bucket. Also set as `MINIO_ACCESS_KEY` above. |
| `MINIO_APP_PASSWORD` | IaC workflow | Secret key for the scoped application user. Also set as `MINIO_SECRET_KEY` above. |
| `MINIO_BACKUP_USER` | IaC workflow | Access key for the scoped backup user, provisioned by Terraform with access to only `billpiggy-backups`. Used solely by the in-cluster backup `CronJob`s. |
| `MINIO_BACKUP_PASSWORD` | IaC workflow | Secret key for the scoped backup user. |
| `TF_STATE_BUCKET` | IaC workflow | Cloudflare R2 bucket name holding Terraform state. |
| `TF_STATE_KEY` | IaC workflow | State object key/path within that bucket, e.g. `billpiggy/terraform.tfstate`. |
| `TF_STATE_ENDPOINT` | IaC workflow | R2 bucket's S3 API endpoint: `https://<ACCOUNT_ID>.r2.cloudflarestorage.com`. |
| `TF_STATE_ACCESS_KEY_ID` | IaC workflow | Access key ID from an R2 API token scoped to only the state bucket. |
| `TF_STATE_SECRET_ACCESS_KEY` | IaC workflow | Secret access key for the same token. |

Keep the bootstrap credentials available after initial deployment. They are only used
when no super-admin exists, but are needed if a newly provisioned database must be
bootstrapped.

The notification worker caps outgoing email at `MAILERSEND_MONTHLY_LIMIT` (default `500`,
matching MailerSend's free tier) per rolling 30-day window, shared across every recipient.
Once reached, queued emails wait rather than fail permanently: sending resumes automatically
as the window rolls over. This isn't currently exposed through the deploy workflow or Helm
values — it's a plain container env var, so override it by adding it to the Secret/values
alongside the `MAILERSEND_*` entries above if a paid plan raises the real quota.

## GitHub Environment or repository variables

| Variable | Required | Example |
| --- | --- | --- |
| `DOCKER_IMAGE_NAME` | Recommended | `ownerofglory/billpiggy` |
| `INGRESS_HOST` | Yes | `billpiggy.example.com` |
| `INGRESS_CLASS_NAME` | Optional | `nginx` |
| `INGRESS_CLUSTER_ISSUER` | Yes | `letsencrypt-prod` |
| `INGRESS_TLS_SECRET_NAME` | Yes | `billpiggy-tls` |
| `LOG_LEVEL` | Optional | `info` |
| `PUBLIC_BASE_URL` | Optional | `https://app.billpiggy.example.com`. The externally reachable app (frontend) URL — may differ from `INGRESS_HOST`, which is this API's own host. Used to build links in outgoing email, e.g. an invitation's accept link. Without it, those emails carry a raw code instead of a link. |
| `INFRASTRUCTURE_NAMESPACE` | IaC workflow | `billpiggy-infra` |
| `K3S_STORAGE_CLASS` | IaC workflow | `local-path` |
| `POSTGRES_BACKUP_SCHEDULE` | Optional | `0 3 * * *` |
| `MINIO_BACKUP_SCHEDULE` | Optional | `30 3 * * *` |
| `BACKUP_RETENTION_DAYS` | Optional | `14` |
| `MINIO_BUCKET` | Optional | `billpiggy` |
| `MINIO_USE_SSL` | Optional | `false` for the in-cluster MinIO service |
| `OPENAI_ASSISTANT_MODEL` | Optional | `gpt-5.6-luna` |
| `OPENAI_BASE_URL` | Optional | Empty routes to `api.openai.com`; set to point the assistant at a compatible gateway instead. |
| `CORS_ALLOWED_ORIGINS` | Yes | `https://billpiggy.example.com`. Comma-separated if the frontend is served from more than one origin (e.g. a preview deployment). Required because the frontend's `credentials: "include"` requests are blocked at the browser's preflight stage without it — see [Cross-origin access](#cross-origin-access). |

## Database migrations

Run the manual **Apply database migrations** workflow before the first application
deployment and whenever a release adds a migration. It runs a short-lived Helm hook
Job inside the cluster, so the cluster-internal PostgreSQL service never needs to be
exposed to a GitHub-hosted runner. Provide the release image tag and type `MIGRATE`
to confirm. The job records applied files in `public.schema_migrations` and is safe to
run again for the same image.

The application chart itself also gates on this: a `pre-install`/`pre-upgrade` hook
Job (`migrations-check`, enabled by default via `migrationsCheck.enabled`) fails the
release outright if the target image expects migrations the database doesn't have
yet, so `helm upgrade` can't silently roll out a schema-incompatible deployment.

## Infrastructure provisioning

The [Terraform infrastructure root](../infra/terraform) provisions the initial
PostgreSQL and MinIO releases for k3s. Use its protected manual workflow before
deploying the application. It needs additional Terraform-state and dependency
credentials; see its [README](../infra/terraform/README.md).

Choose `plan` first. The `apply` action requires the literal confirmation `APPLY`.
Terraform apply is intentionally prohibited on local machines; only the protected
GitHub Actions workflow is authorized to make infrastructure changes.

If a `plan`/`apply` run fails with "Error acquiring the state lock" — typically left
behind by cancelling a previous run from the GitHub UI mid-apply rather than letting
it fail on its own — run the **Unlock Terraform state** workflow with the `LOCK_ID`
from that error message and `UNLOCK` typed to confirm. It only removes the lock
marker; it never touches state content or infrastructure.

## Run a deployment

### Automatic: the release train

Publishing a GitHub Release (e.g. tag `v1.0.0`) is normally all that's needed. That
tag push already triggers the Docker release workflow and the Helm release workflow
in parallel, publishing image `1.0.0` and chart `1.0.0` (the leading `v` is stripped
from both). The **Release train** workflow (`release-train.yaml`) then runs
automatically on the release's `published` event:

1. Resolves the version from the release tag and polls both registries until the
   image and the chart for that exact version exist — this doesn't assume either of
   the two build workflows finishes first, and fails with a clear message if a build
   never completes rather than deploying half of a release.
2. Calls **Apply database migrations** for that image tag.
3. Calls **Deploy Kubernetes production** with `chart_version` and `image_tag` both
   set to the resolved version.
4. Calls **Post-deploy smoke test**.

Each of those is the same workflow a human would run by hand (see below) — the
release train calls them as reusable workflows (`workflow_call`) rather than
duplicating their steps, so there's exactly one place each step is defined.

### Manual: for a feature branch, a hotfix, or re-running one step

The three workflows the release train calls remain fully usable on their own via
`workflow_dispatch` — useful for deploying an unreleased build, retrying just one
step, or trying out a feature branch's image/chart without cutting a release:

1. Publish the image through the Docker release workflow and the chart through the
   Helm release workflow (either by pushing a `v*` tag, or by pushing to `main`,
   which publishes a `0.1.0-main.<short-sha>` build).
2. Run **Apply database migrations**, providing the image tag and typing `MIGRATE`
   to confirm.
3. Run **Deploy Kubernetes production**, providing:
   - `chart_version`: the published chart version, without a leading `v`;
   - `image_tag`: the image tag created by the Docker workflow.

The deployment waits up to five minutes for the Helm release. The application probes
`/livez`, `/readyz`, and `/startupz`; a deployment is not ready until its required
runtime dependencies are ready. The chart's own pre-upgrade hook
(`migrations-check-job.yaml`) refuses the release outright if any migration is still
pending, regardless of which path — automatic or manual — got you there.

The **Post-deploy smoke test** workflow runs automatically after a successful
**Deploy Kubernetes production** *manual* run (or on demand via `workflow_dispatch`)
and curls those same three endpoints over the public ingress, so a deploy that
reports success in-cluster but isn't actually reachable gets caught immediately. The
release train calls it directly as a fourth step instead, since `workflow_run`
doesn't fire for a `workflow_call` invocation.

## Restart the service

The **Restart production service** workflow (`restart-production.yaml`, manual
`workflow_dispatch` only) scales the Deployment to 0 replicas, waits for its pods to
actually terminate, then scales back to 1 and waits for the new pod to become ready.
Useful for clearing a stuck process — e.g. one wedged on a hung external
call — without a new image or a full redeploy.

## Cross-origin access

The frontend is never served from this API's own origin, so every request it makes
with `credentials: "include"` (needed for the refresh-token cookie) is a credentialed
cross-origin request. Browsers gate those behind a preflight `OPTIONS` request; without
`CORS_ALLOWED_ORIGINS` set to the frontend's exact origin(s), the API answers preflight
with a plain `405` and the browser blocks every real request before it's ever sent —
`config.Validate()` refuses to start the app in production without this set, so a
missing value fails the deployment instead of silently shipping a broken CORS setup.

Origins must be listed explicitly (`scheme://host`, no path, no trailing slash);
a wildcard `*` cannot be combined with credentialed requests per the CORS spec, so there
is no permissive fallback here by design.

## Secret handling

`existingSecret` is the default and recommended path: the **Deploy Kubernetes
production** workflow's "Apply application secret" step `kubectl apply`s a `Secret`
named `billpiggy-app` directly, outside Helm, then deploys the chart with
`existingSecret=billpiggy-app`. Application secret values therefore never appear in
`helm get values` or release metadata — only in the Secret object itself.

If you deploy the chart some other way (e.g. a custom pipeline) and leave
`existingSecret` empty, the chart falls back to creating a release-owned Secret from
the `secrets.*` values instead, which does put those values in Helm release metadata.
Prefer `existingSecret` whenever you control how the Secret is created; reserve the
fallback for quick local/dev installs. Either way, the Secret must contain the exact
keys listed in
[`charts/billpiggy/templates/secret.yaml`](../charts/billpiggy/templates/secret.yaml).
