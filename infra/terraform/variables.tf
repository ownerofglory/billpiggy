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
  description = "MinIO administrative access key."
  type        = string
  sensitive   = true
}

variable "minio_root_password" {
  description = "MinIO administrative secret key."
  type        = string
  sensitive   = true
}
