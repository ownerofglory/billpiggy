variable "kubeconfig_path" {
  description = "Path to a kubeconfig available to Terraform."
  type        = string
  default     = "~/.kube/config"
}

variable "namespace" {
  description = "Namespace that owns PostgreSQL and MinIO."
  type        = string
  default     = "billpiggy-infra"
}

variable "storage_class" {
  description = "Persistent volume storage class; k3s defaults to local-path."
  type        = string
  default     = "local-path"
}

variable "postgres_password" {
  description = "Password for the BillPiggy PostgreSQL application user."
  type        = string
  sensitive   = true
}

variable "minio_root_user" {
  description = "MinIO administrative access key. Used only to bootstrap the cluster; the application uses minio_app_user instead."
  type        = string
  sensitive   = true
}

variable "minio_root_password" {
  description = "MinIO administrative secret key."
  type        = string
  sensitive   = true
}

variable "minio_app_user" {
  description = "Access key for the scoped application user, provisioned with access to only the billpiggy bucket. This is what MINIO_ACCESS_KEY should be set to."
  type        = string
  sensitive   = true
}

variable "minio_app_password" {
  description = "Secret key for the scoped application user. This is what MINIO_SECRET_KEY should be set to."
  type        = string
  sensitive   = true
}

variable "minio_backup_user" {
  description = "Access key for the scoped backup user, provisioned with access to only the billpiggy-backups bucket. Used solely by the in-cluster backup CronJobs, never by the application."
  type        = string
  sensitive   = true
}

variable "minio_backup_password" {
  description = "Secret key for the scoped backup user."
  type        = string
  sensitive   = true
}

variable "postgres_backup_schedule" {
  description = "Cron schedule (in the cluster's UTC clock) for the nightly PostgreSQL dump CronJob."
  type        = string
  default     = "0 3 * * *"
}

variable "minio_backup_schedule" {
  description = "Cron schedule for the CronJob that mirrors the billpiggy bucket into billpiggy-backups."
  type        = string
  default     = "30 3 * * *"
}

variable "backup_retention_days" {
  description = "Number of days of PostgreSQL dumps to retain in billpiggy-backups before the nightly job prunes them. Does not affect the MinIO mirror, which always reflects the live bucket."
  type        = number
  default     = 14
}
