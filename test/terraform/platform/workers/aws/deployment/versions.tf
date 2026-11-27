terraform {
  required_version = ">= 1.16.0"

  # The lab coordinator supplies an owner-private state path.
  backend "local" {}

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "6.74.0"
    }
    helm = {
      source  = "hashicorp/helm"
      version = "3.3.0"
    }
    http = {
      source  = "hashicorp/http"
      version = "3.6.3"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "3.5.0"
    }
  }
}

provider "aws" {
  region              = var.cloud.identifiers.region
  allowed_account_ids = [var.cloud.identifiers.account_id]

  default_tags {
    tags = merge(var.ownership.labels, { "io.infraweave.lab.expires-at" = var.ownership.expires_at })
  }
}

# Kubernetes and Helm reach the cluster stage's endpoint, trusting only its
# CA, with a token the AWS CLI mints from the caller's credentials for each
# connection, so a long install never outlives a 15-minute token. The
# caller's identity holds cluster-admin through the access entry EKS gave
# the cluster's creator.
locals {
  kubernetes_exec = {
    api_version = "client.authentication.k8s.io/v1beta1"
    command     = "aws"
    args        = ["eks", "get-token", "--cluster-name", var.cluster_stage.name, "--region", var.cloud.identifiers.region, "--output", "json"]
  }
}

provider "kubernetes" {
  host                   = var.cluster_stage.endpoint
  cluster_ca_certificate = var.cluster_stage.ca_certificate

  exec {
    api_version = local.kubernetes_exec.api_version
    command     = local.kubernetes_exec.command
    args        = local.kubernetes_exec.args
  }
}

provider "helm" {
  kubernetes = {
    host                   = var.cluster_stage.endpoint
    cluster_ca_certificate = var.cluster_stage.ca_certificate
    exec                   = local.kubernetes_exec
  }
}
