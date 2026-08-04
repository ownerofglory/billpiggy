output "postgresql_host" {
  description = "In-cluster PostgreSQL service hostname."
  value       = "${helm_release.postgresql.name}.${var.namespace}.svc.cluster.local"
}

output "minio_endpoint" {
  description = "In-cluster MinIO endpoint."
  value       = "http://${helm_release.minio.name}.${var.namespace}.svc.cluster.local:9000"
}

output "backup_bucket" {
  description = "Bucket holding nightly PostgreSQL dumps and the billpiggy mirror; see docs/backup-and-disaster-recovery.md."
  value       = "billpiggy-backups"
}
