resource "kubernetes_namespace_v1" "infrastructure" {
  metadata {
    name = var.namespace
  }
}

resource "helm_release" "postgresql" {
  name = "billpiggy-postgresql"
  # Bitnami retired the classic https://charts.bitnami.com/bitnami index on
  # 2025-08-28; charts now live only as OCI artifacts. Pointing helm_release
  # at the old index either 404s or resolves to a malformed OCI reference
  # depending on which chart, which is why postgresql failed with
  # "could not download chart: invalid_reference: invalid tag" while minio
  # below merely hung until its install timed out — same root cause,
  # different failure shape. 18.8.6 is a real published tag as of this
  # change (there is no 16.x left in the OCI repo to fall back to); it's a
  # jump across major chart versions from the old 16.4.5 pin, so review
  # https://github.com/bitnami/charts/blob/main/bitnami/postgresql/README.md
  # for values.yaml changes before applying to an environment that already
  # has data — this repo has none yet.
  repository = "oci://registry-1.docker.io/bitnamicharts"
  chart      = "postgresql"
  version    = "18.8.6"
  namespace  = kubernetes_namespace_v1.infrastructure.metadata[0].name

  set {
    name  = "auth.username"
    value = "billpiggy"
  }
  set_sensitive {
    name  = "auth.password"
    value = var.postgres_password
  }
  set {
    name  = "auth.database"
    value = "billpiggy"
  }
  set_sensitive {
    name  = "auth.postgresPassword"
    value = var.postgres_password
  }
  set {
    name  = "primary.persistence.storageClass"
    value = var.storage_class
  }
  set {
    name  = "primary.persistence.size"
    value = "8Gi"
  }
  set {
    name  = "primary.resources.requests.memory"
    value = "256Mi"
  }
  set {
    name  = "primary.resources.requests.cpu"
    value = "100m"
  }
}

resource "helm_release" "minio" {
  name = "billpiggy-minio"
  # See the comment on helm_release.postgresql above: same OCI migration,
  # same fix. 17.0.21 is the newest minio chart tag published under the new
  # OCI path as of this change, itself a jump from the retired 14.7.3.
  repository = "oci://registry-1.docker.io/bitnamicharts"
  chart      = "minio"
  version    = "17.0.21"
  namespace  = kubernetes_namespace_v1.infrastructure.metadata[0].name

  # Two applies in a row hit "Still creating..." for the full 5m20s before
  # timing out — once against the broken pre-OCI-migration chart source,
  # once against the now-working one — so the timeout itself, not the chart
  # version, is the current suspect. Raised from the 300s default; if it
  # still times out at 600s, the pod is not merely slow (mode still defaults
  # to standalone here, and its readiness/liveness probes fire 5s after the
  # container starts and poll every 5s, so a container that's actually
  # Running would pass them almost immediately) — that needs `kubectl get
  # pods`/`describe`/`logs` in the cluster to see whether it's stuck
  # Pending, ImagePullBackOff, or CrashLoopBackOff, none of which are
  # visible from here.
  timeout = 600
  # Also gates completion on the provisioning Job succeeding, not just the
  # MinIO pod being ready. Without this (the provider default), a
  # provisioning Job that silently failed would leave this apply reporting
  # success while the billpiggy-app/billpiggy-backup users — which
  # everything else in this file assumes exist — were never actually
  # created.
  wait_for_jobs = true

  # Non-sensitive values, including the policy that scopes the application
  # user below to the billpiggy bucket only. Root credentials and the
  # provisioned user's own credentials are supplied separately as
  # set_sensitive so they never appear in this file or in plan/state diffs.
  values = [
    yamlencode({
      # Broadcom pruned versioned tags from the free docker.io/bitnami/*
      # image namespace on 2025-08-28 — a separate, deeper break than the
      # chart-hosting migration above, confirmed by a real ImagePullBackOff:
      # "docker.io/bitnami/minio:2025.7.23-debian-12-r3: not found". The
      # frozen (no further updates) mirror of exactly these pre-migration
      # tags lives at docker.io/bitnamilegacy/*; both tags below were
      # confirmed present there before this override was written. Two
      # separate image keys need overriding, not one: `image` is the main
      # MinIO server container, `clientImage` is what the provisioning Job
      # below actually runs (the chart does not reuse `image` for it).
      image = {
        registry   = "docker.io"
        repository = "bitnamilegacy/minio"
      }
      clientImage = {
        registry   = "docker.io"
        repository = "bitnamilegacy/minio-client"
      }
      # billpiggy-backups holds nightly PostgreSQL dumps and a mirror of the
      # billpiggy bucket; see the backup CronJobs below and
      # docs/backup-and-disaster-recovery.md.
      defaultBuckets = "billpiggy,billpiggy-backups"
      persistence = {
        storageClass = var.storage_class
        size         = "20Gi"
      }
      resources = {
        requests = {
          memory = "256Mi"
          cpu    = "100m"
        }
      }
      # The Bitnami chart runs a provisioning Job on install/upgrade that
      # creates these policies and users through `mc admin`, so neither the
      # application nor the backup CronJobs ever need the MinIO root
      # credentials — only auth.rootUser/auth.rootPassword below do, and
      # those stay operator-only secrets.
      provisioning = {
        enabled = true
        policies = [
          {
            name = "billpiggy-app"
            statements = [
              {
                resources = [
                  "arn:aws:s3:::billpiggy",
                  "arn:aws:s3:::billpiggy/*",
                ]
                actions = [
                  "s3:GetObject",
                  "s3:PutObject",
                  "s3:DeleteObject",
                  "s3:ListBucket",
                ]
                effect = "Allow"
              }
            ]
          },
          {
            name = "billpiggy-backup"
            statements = [
              {
                resources = [
                  "arn:aws:s3:::billpiggy-backups",
                  "arn:aws:s3:::billpiggy-backups/*",
                ]
                actions = [
                  "s3:GetObject",
                  "s3:PutObject",
                  "s3:DeleteObject",
                  "s3:ListBucket",
                ]
                effect = "Allow"
              },
              {
                # The bucket mirror CronJob only ever reads from billpiggy;
                # it must never be able to write to or delete from it.
                resources = [
                  "arn:aws:s3:::billpiggy",
                  "arn:aws:s3:::billpiggy/*",
                ]
                actions = [
                  "s3:GetObject",
                  "s3:ListBucket",
                ]
                effect = "Allow"
              }
            ]
          }
        ]
      }
    })
  ]

  set_sensitive {
    name  = "auth.rootUser"
    value = var.minio_root_user
  }
  set_sensitive {
    name  = "auth.rootPassword"
    value = var.minio_root_password
  }
  set_sensitive {
    name  = "provisioning.users[0].username"
    value = var.minio_app_user
  }
  set_sensitive {
    name  = "provisioning.users[0].password"
    value = var.minio_app_password
  }
  set {
    name  = "provisioning.users[0].disabled"
    value = "false"
  }
  set {
    name  = "provisioning.users[0].policies[0]"
    value = "billpiggy-app"
  }
  set_sensitive {
    name  = "provisioning.users[1].username"
    value = var.minio_backup_user
  }
  set_sensitive {
    name  = "provisioning.users[1].password"
    value = var.minio_backup_password
  }
  set {
    name  = "provisioning.users[1].disabled"
    value = "false"
  }
  set {
    name  = "provisioning.users[1].policies[0]"
    value = "billpiggy-backup"
  }
}

resource "kubernetes_cron_job_v1" "postgres_backup" {
  metadata {
    name      = "billpiggy-postgres-backup"
    namespace = kubernetes_namespace_v1.infrastructure.metadata[0].name
  }

  spec {
    schedule                      = var.postgres_backup_schedule
    concurrency_policy            = "Forbid"
    successful_jobs_history_limit = 3
    failed_jobs_history_limit     = 3

    job_template {
      metadata {}
      spec {
        backoff_limit = 2
        template {
          metadata {}
          spec {
            restart_policy = "OnFailure"

            volume {
              name = "dump"
              empty_dir {
                size_limit = "1Gi"
              }
            }

            # pg_dump and the mc upload run as separate containers sharing an
            # emptyDir so neither image needs both tools installed at
            # runtime; both are stock upstream images, never built or pushed
            # from here.
            init_container {
              name  = "pg-dump"
              image = "postgres:16-alpine"
              command = ["/bin/sh", "-c", <<-EOT
                set -eu
                stamp=$(date -u +%Y%m%dT%H%M%SZ)
                PGPASSWORD="$POSTGRES_PASSWORD" pg_dump \
                  -h "${helm_release.postgresql.name}.${var.namespace}.svc.cluster.local" \
                  -U billpiggy -d billpiggy \
                  | gzip > "/dump/billpiggy-$${stamp}.sql.gz"
              EOT
              ]
              env {
                name  = "POSTGRES_PASSWORD"
                value = var.postgres_password
              }
              volume_mount {
                name       = "dump"
                mount_path = "/dump"
              }
            }

            container {
              name  = "upload"
              image = "minio/mc:RELEASE.2025-04-16T18-13-26Z"
              command = ["/bin/sh", "-c", <<-EOT
                set -eu
                mc alias set backup "$MINIO_ENDPOINT" "$MINIO_ACCESS_KEY" "$MINIO_SECRET_KEY"
                for f in /dump/*; do
                  mc cp "$f" "backup/billpiggy-backups/postgres/$(basename "$f")"
                done
                mc rm --recursive --force --older-than "$${RETENTION_DAYS}d" backup/billpiggy-backups/postgres/ || true
              EOT
              ]
              env {
                name  = "MINIO_ENDPOINT"
                value = "http://${helm_release.minio.name}.${var.namespace}.svc.cluster.local:9000"
              }
              env {
                name  = "MINIO_ACCESS_KEY"
                value = var.minio_backup_user
              }
              env {
                name  = "MINIO_SECRET_KEY"
                value = var.minio_backup_password
              }
              env {
                name  = "RETENTION_DAYS"
                value = tostring(var.backup_retention_days)
              }
              volume_mount {
                name       = "dump"
                mount_path = "/dump"
              }
            }
          }
        }
      }
    }
  }
}

resource "kubernetes_cron_job_v1" "minio_backup_mirror" {
  metadata {
    name      = "billpiggy-minio-backup-mirror"
    namespace = kubernetes_namespace_v1.infrastructure.metadata[0].name
  }

  spec {
    schedule                      = var.minio_backup_schedule
    concurrency_policy            = "Forbid"
    successful_jobs_history_limit = 3
    failed_jobs_history_limit     = 3

    job_template {
      metadata {}
      spec {
        backoff_limit = 2
        template {
          metadata {}
          spec {
            restart_policy = "OnFailure"
            container {
              name  = "mirror"
              image = "minio/mc:RELEASE.2025-04-16T18-13-26Z"
              command = ["/bin/sh", "-c", <<-EOT
                set -eu
                mc alias set backup "$MINIO_ENDPOINT" "$MINIO_ACCESS_KEY" "$MINIO_SECRET_KEY"
                mc mirror --overwrite --remove backup/billpiggy backup/billpiggy-backups/mirror
              EOT
              ]
              env {
                name  = "MINIO_ENDPOINT"
                value = "http://${helm_release.minio.name}.${var.namespace}.svc.cluster.local:9000"
              }
              env {
                name  = "MINIO_ACCESS_KEY"
                value = var.minio_backup_user
              }
              env {
                name  = "MINIO_SECRET_KEY"
                value = var.minio_backup_password
              }
            }
          }
        }
      }
    }
  }
}
