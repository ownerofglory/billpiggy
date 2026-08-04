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
