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
| `SMTP_ADDRESS` | Optional | SMTP relay address including port. |
| `SMTP_USERNAME` | Optional | SMTP login user. |
| `SMTP_PASSWORD` | Optional | SMTP login password. |
| `SMTP_FROM` | Optional | Sender address for BillPiggy notification email. |
| `MINIO_ENDPOINT` | Yes | In-cluster MinIO endpoint, e.g. `billpiggy-minio.billpiggy-infra.svc.cluster.local:9000`. |
| `MINIO_ACCESS_KEY` | Yes | MinIO application access key; use the initial root user only until a scoped user is provisioned. |
| `MINIO_SECRET_KEY` | Yes | MinIO application secret key. |
| `DOCKER_USER` | Image workflows | Docker Hub user used as the default image namespace. |
| `DOCKER_TOKEN` | Image workflows | Docker Hub access token. |
| `POSTGRES_PASSWORD` | IaC workflow | Password for the PostgreSQL application and administrative users. |
| `MINIO_ROOT_USER` | IaC workflow | MinIO root access key. |
| `MINIO_ROOT_PASSWORD` | IaC workflow | MinIO root secret key. |
| `TF_HTTP_ADDRESS` | IaC workflow | HTTP Terraform state endpoint. |
| `TF_HTTP_USERNAME` | IaC workflow | HTTP state backend user, if required. |
| `TF_HTTP_PASSWORD` | IaC workflow | HTTP state backend password, if required. |
| `TF_HTTP_LOCK_ADDRESS` | IaC workflow | HTTP state-lock endpoint, if supported. |
| `TF_HTTP_UNLOCK_ADDRESS` | IaC workflow | HTTP state-unlock endpoint, if supported. |

Keep the bootstrap credentials available after initial deployment. They are only used
when no super-admin exists, but are needed if a newly provisioned database must be
bootstrapped.

## GitHub Environment or repository variables

| Variable | Required | Example |
| --- | --- | --- |
| `DOCKER_IMAGE_NAME` | Recommended | `ownerofglory/billpiggy` |
| `INGRESS_HOST` | Yes | `billpiggy.example.com` |
| `INGRESS_CLASS_NAME` | Optional | `nginx` |
| `INGRESS_CLUSTER_ISSUER` | Yes | `letsencrypt-prod` |
| `INGRESS_TLS_SECRET_NAME` | Yes | `billpiggy-tls` |
| `LOG_LEVEL` | Optional | `info` |
| `INFRASTRUCTURE_NAMESPACE` | IaC workflow | `billpiggy-infra` |
| `K3S_STORAGE_CLASS` | IaC workflow | `local-path` |
| `MINIO_BUCKET` | Optional | `billpiggy` |
| `MINIO_USE_SSL` | Optional | `false` for the in-cluster MinIO service |

## Database migrations

Run the manual **Apply database migrations** workflow before the first application
deployment and whenever a release adds a migration. It runs a short-lived Helm hook
Job inside the cluster, so the cluster-internal PostgreSQL service never needs to be
exposed to a GitHub-hosted runner. Provide the release image tag and type `MIGRATE`
to confirm. The job records applied files in `public.schema_migrations` and is safe to
run again for the same image.

## Infrastructure provisioning

The [Terraform infrastructure root](../infra/terraform) provisions the initial
PostgreSQL and MinIO releases for k3s. Use its protected manual workflow before
deploying the application. It needs additional Terraform-state and dependency
credentials; see its [README](../infra/terraform/README.md).

Choose `plan` first. The `apply` action requires the literal confirmation `APPLY`.
Terraform apply is intentionally prohibited on local machines; only the protected
GitHub Actions workflow is authorized to make infrastructure changes.

## Run a deployment

1. Publish the release image through the Docker release workflow.
2. Publish the matching Helm chart through the Helm release workflow.
3. Run **Apply database migrations** for that image tag.
4. Run **Deploy Kubernetes production** manually and provide:
   - `chart_version`: the published chart version, without a leading `v`;
   - `image_tag`: the image tag created by the Docker workflow.

The deployment waits up to five minutes for the Helm release. The application probes
`/livez`, `/readyz`, and `/startupz`; a deployment is not ready until its required
runtime dependencies are ready.

## Secret handling

By default, the chart creates a release-owned Kubernetes Secret from the GitHub
Environment secrets. Helm release metadata therefore contains the application values
in the cluster. For a stricter separation of duties, create the application Secret
through your infrastructure provisioning process and deploy with `existingSecret`
set to that Secret name instead. In that mode the Secret must contain the exact keys
listed in [`charts/billpiggy/templates/secret.yaml`](../charts/billpiggy/templates/secret.yaml).
