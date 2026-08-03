# BillPiggy production infrastructure

This Terraform root provisions the low-cost stateful dependencies for the initial
k3s deployment:

- single-primary PostgreSQL with an 8 GiB `local-path` volume;
- MinIO with a 20 GiB `local-path` volume and the `billpiggy` bucket.

It uses Helm and Kubernetes providers rather than shelling out to cluster commands.
The configuration has no cluster credentials or passwords committed to the repository.

## State and GitHub Actions

The manual **Apply production infrastructure** workflow requires an HTTP Terraform
state backend. Configure `TF_HTTP_ADDRESS`, `TF_HTTP_USERNAME`, and
`TF_HTTP_PASSWORD` as `production` Environment secrets. If the backend supports
locking, also configure `TF_HTTP_LOCK_ADDRESS` and `TF_HTTP_UNLOCK_ADDRESS`.

The workflow additionally requires `POSTGRES_PASSWORD`, `MINIO_ROOT_USER`, and
`MINIO_ROOT_PASSWORD` Environment secrets, plus the Kubernetes credentials listed in
the [production deployment guide](../../docs/production-deployment.md). Its `apply`
action runs only when the `confirm` input is exactly `APPLY`.

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
