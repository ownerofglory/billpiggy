# BillPiggy production infrastructure

This Terraform root provisions the low-cost stateful dependencies for the initial
k3s deployment:

- single-primary PostgreSQL with an 8 GiB `local-path` volume;
- MinIO with a 20 GiB `local-path` volume and the `billpiggy` bucket.

It uses Helm and Kubernetes providers rather than shelling out to cluster commands.
The configuration has no cluster credentials or passwords committed to the repository.

It also provisions a `billpiggy-backups` bucket and two `CronJob`s: a nightly
PostgreSQL dump uploaded to that bucket, and a periodic mirror of the `billpiggy`
bucket into it. See [the backup and DR doc](../../docs/backup-and-disaster-recovery.md)
for what's covered, restore steps, and current limitations.

## State and GitHub Actions

Terraform state is stored in a [Cloudflare R2](https://developers.cloudflare.com/r2/)
bucket, addressed through Terraform's `s3` backend — R2 exposes an S3-compatible API,
so no separate backend implementation is needed. `versions.tf` declares an empty
`backend "s3" {}`; the manual **Apply production infrastructure** workflow renders
[`backend.hcl.tmpl`](backend.hcl.tmpl) with `envsubst` and passes it to
`terraform init -backend-config=...`, so nothing bucket- or account-specific is
committed to the repository.

Configure these as `production` Environment secrets:

| Secret | Purpose |
| --- | --- |
| `TF_STATE_BUCKET` | R2 bucket name holding the state object. |
| `TF_STATE_KEY` | Object key/path within the bucket, e.g. `billpiggy/terraform.tfstate`. |
| `TF_STATE_ENDPOINT` | The bucket's S3 API endpoint: `https://<ACCOUNT_ID>.r2.cloudflarestorage.com`. |
| `TF_STATE_ACCESS_KEY_ID` | Access key ID from an R2 API token scoped to that bucket. |
| `TF_STATE_SECRET_ACCESS_KEY` | Secret access key for the same token. |

To create these in Cloudflare: R2 → create a bucket for state → **Manage API tokens**
→ create a token scoped to **only that bucket** with **Object Read & Write**
permission (not the account-wide R2 token, and not the Cloudflare account API token).
The dashboard shows the Access Key ID/Secret Access Key once, at creation time, and
the Jurisdiction-specific S3 API endpoint to use as `TF_STATE_ENDPOINT`.

State locking uses the S3 backend's native conditional-write locking
(`use_lockfile = true` in `backend.hcl.tmpl`) rather than the old DynamoDB-table
mechanism, which R2 has no equivalent for. This needs Terraform >= 1.11 (pinned in
`versions.tf`) and an R2 bucket, both already satisfied by this setup; if a future
`terraform init` ever rejects `use_lockfile` as unsupported, removing that one line
degrades to unlocked state rather than breaking the workflow.

Credentials are read by the S3 backend's AWS SDK from the standard
`AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` environment variables (set by the workflow
from the two secrets above) rather than written into `backend.hcl`, so no credential
ever lands in a file on the runner's disk.

The workflow additionally requires `POSTGRES_PASSWORD`, `MINIO_ROOT_USER`,
`MINIO_ROOT_PASSWORD`, `MINIO_APP_USER`, and `MINIO_APP_PASSWORD` Environment secrets,
plus the Kubernetes credentials listed in the
[production deployment guide](../../docs/production-deployment.md). Its `apply` action
runs only when the `confirm` input is exactly `APPLY`.

The MinIO release provisions a scoped application user through the chart's built-in
provisioning job, with a policy limited to the `billpiggy` bucket. Use
`MINIO_APP_USER`/`MINIO_APP_PASSWORD` — not the root credentials — for the
application's `MINIO_ACCESS_KEY`/`MINIO_SECRET_KEY`. The root credentials remain
operator-only.

Optional Environment variables:

| Variable | Default | Purpose |
| --- | --- | --- |
| `INFRASTRUCTURE_NAMESPACE` | `billpiggy-infra` | Namespace for PostgreSQL and MinIO. |
| `K3S_STORAGE_CLASS` | `local-path` | k3s persistent-volume storage class. |

After a successful apply, use the Terraform outputs and the PostgreSQL password to
construct `DATABASE_URL` for the application deployment. The in-cluster hostname is
`billpiggy-postgresql.billpiggy-infra.svc.cluster.local` by default.

Run `terraform fmt -check` and `terraform validate` before changing these files. Do
not run `terraform apply` locally against production; use the protected workflow.

## Local setup (break-glass only, e.g. force-unlocking a stuck state lock)

`plan`/`apply` only ever run through the protected workflow — this is for the rare
case you need a state-only command like `terraform force-unlock` directly, when a
prior workflow run acquired the lock and didn't release it cleanly (most commonly:
cancelling a run from the GitHub UI mid-`apply` skips the release). This needs the
same credentials the workflow has — it grants nothing extra — and Terraform >= 1.11:

```sh
cd infra/terraform
export TF_STATE_BUCKET=...           # same value as the TF_STATE_BUCKET secret
export TF_STATE_KEY=...              # same value as the TF_STATE_KEY secret
export TF_STATE_ENDPOINT=...         # same value as the TF_STATE_ENDPOINT secret
export AWS_ACCESS_KEY_ID=...         # same value as the TF_STATE_ACCESS_KEY_ID secret
export AWS_SECRET_ACCESS_KEY=...     # same value as the TF_STATE_SECRET_ACCESS_KEY secret

envsubst < backend.hcl.tmpl > backend.hcl   # gitignored — never commit this file
terraform init -backend-config=backend.hcl
```

Then `terraform force-unlock -force <LOCK_ID>` — the ID is printed in the "Error
acquiring the state lock" message the stuck `plan`/`apply` failed with. This only
removes the lock marker; it does not read, write, or otherwise touch state content or
infrastructure. Alternatively, trigger the **Unlock Terraform state** workflow instead
of setting any of this up locally — same operation, run through the protected
workflow, no local Terraform install needed.
