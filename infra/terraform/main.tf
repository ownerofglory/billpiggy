resource "kubernetes_namespace_v1" "infrastructure" {
  metadata {
    name = var.namespace
  }
}

resource "helm_release" "postgresql" {
  name       = "billpiggy-postgresql"
  repository = "https://charts.bitnami.com/bitnami"
  chart      = "postgresql"
  version    = "16.4.5"
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
  name       = "billpiggy-minio"
  repository = "https://charts.bitnami.com/bitnami"
  chart      = "minio"
  version    = "14.7.3"
  namespace  = kubernetes_namespace_v1.infrastructure.metadata[0].name

  # Non-sensitive values, including the policy that scopes the application
  # user below to the billpiggy bucket only. Root credentials and the
  # provisioned user's own credentials are supplied separately as
  # set_sensitive so they never appear in this file or in plan/state diffs.
  values = [
    yamlencode({
      defaultBuckets = "billpiggy"
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
      # creates this policy and user through `mc admin`, so the application
      # never needs the MinIO root credentials — only auth.rootUser /
      # auth.rootPassword below do, and those stay operator-only secrets.
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
}
