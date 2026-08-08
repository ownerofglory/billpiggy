terraform {
  # 1.11 is required for the S3 backend's native `use_lock_file` locking
  # (conditional writes) used below — the state backend is Cloudflare R2's
  # S3-compatible API, which has no DynamoDB equivalent for the older
  # lock-table mechanism.
  required_version = ">= 1.11.0"

  # Empty on purpose: bucket/key/endpoint and credentials are supplied at
  # `terraform init` time via `-backend-config`, so nothing endpoint- or
  # account-specific is committed here. See infra/terraform/README.md.
  backend "s3" {}

  required_providers {
    helm = {
      source  = "hashicorp/helm"
      version = "~> 2.16"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 2.36"
    }
  }
}

provider "kubernetes" {
  config_path = var.kubeconfig_path
}

provider "helm" {
  kubernetes {
    config_path = var.kubeconfig_path
  }
}
