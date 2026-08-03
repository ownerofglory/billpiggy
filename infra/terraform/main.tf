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

  set_sensitive {
    name  = "auth.rootUser"
    value = var.minio_root_user
  }
  set_sensitive {
    name  = "auth.rootPassword"
    value = var.minio_root_password
  }
  set {
    name  = "defaultBuckets"
    value = "billpiggy"
  }
  set {
    name  = "persistence.storageClass"
    value = var.storage_class
  }
  set {
    name  = "persistence.size"
    value = "20Gi"
  }
  set {
    name  = "resources.requests.memory"
    value = "256Mi"
  }
  set {
    name  = "resources.requests.cpu"
    value = "100m"
  }
}
