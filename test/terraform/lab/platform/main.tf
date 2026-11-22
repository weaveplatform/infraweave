terraform {
  required_version = ">= 1.17.2"
  backend "local" {}
  required_providers {
    docker = {
      source  = "kreuzwerker/docker"
      version = "4.7.0"
    }
  }
}

provider "docker" {}

variable "owner" {
  type = string
  validation {
    condition     = can(regex("^iw-lab-[a-z0-9-]{1,60}$", var.owner))
    error_message = "Use a unique iw-lab- prefixed owner for this deployment."
  }
}

variable "batch" { type = string }
variable "pki_directory" { type = string }
variable "postgres_image" { type = string }
variable "objectstore_image" { type = string }
variable "gitea_image" { type = string }
variable "kubernetes_enabled" {
  type    = bool
  default = false
}
variable "k3s_image" {
  type    = string
  default = ""
}
variable "k3s_memory" {
  type    = number
  default = 4096
  validation {
    condition     = var.k3s_memory >= 2048 && var.k3s_memory <= 8192
    error_message = "The isolated k3s fixture needs 2048 to 8192 MiB."
  }
}
variable "k3s_cpus" {
  type    = number
  default = 2
  validation {
    condition     = var.k3s_cpus >= 1 && var.k3s_cpus <= 4
    error_message = "The isolated k3s fixture permits one to four CPUs."
  }
}
variable "postgres_connections" {
  type    = number
  default = 1000
}

locals {
  labels = {
    "io.infraweave.lab.owner"      = var.owner
    "io.infraweave.lab.batch"      = var.batch
    "io.infraweave.lab.managed-by" = "terraform"
  }
  kubernetes_namespace = "iw-${substr(sha256(var.owner), 0, 32)}"
  services = {
    postgres = {
      image = var.postgres_image
      port  = 5432
      data  = "/var/lib/postgresql"
      env   = ["POSTGRES_USER=infraweave", "POSTGRES_PASSWORD=infraweave", "POSTGRES_DB=infraweave"]
    }
    objectstore = {
      image = var.objectstore_image
      port  = 9000
      data  = "/data"
      env   = ["RUSTFS_ACCESS_KEY=infraweave", "RUSTFS_SECRET_KEY=infraweave-secret"]
    }
    gitea = {
      image = var.gitea_image
      port  = 3000
      data  = "/data"
      env = [
        "GITEA__security__INSTALL_LOCK=true",
        "GITEA__server__PROTOCOL=https",
        "GITEA__server__CERT_FILE=/data/certs/cert.pem",
        "GITEA__server__KEY_FILE=/data/certs/key.pem",
        "GITEA__server__HTTP_PORT=3000",
        "GITEA__server__DISABLE_SSH=true",
        "GITEA__database__DB_TYPE=sqlite3",
        "GITEA__service__DISABLE_REGISTRATION=true",
        "GITEA__repository__DEFAULT_BRANCH=main",
        "GITEA__log__LEVEL=Warn",
      ]
    }
  }
}

resource "docker_network" "batch" {
  name = var.owner
  dynamic "labels" {
    for_each = local.labels
    content {
      label = labels.key
      value = labels.value
    }
  }
}

resource "docker_volume" "data" {
  for_each = local.services
  name     = "${var.owner}-${each.key}"
  dynamic "labels" {
    for_each = local.labels
    content {
      label = labels.key
      value = labels.value
    }
  }
}

resource "docker_image" "service" {
  for_each     = local.services
  name         = each.value.image
  keep_locally = true
  lifecycle {
    precondition {
      condition     = can(regex("@sha256:[0-9a-f]{64}$", each.value.image))
      error_message = "Lab service images must use committed immutable digest pins."
    }
  }
}

resource "docker_container" "service" {
  for_each       = local.services
  name           = "${var.owner}-${each.key}"
  image          = docker_image.service[each.key].image_id
  env            = each.value.env
  command        = each.key == "postgres" ? ["postgres", "-c", "max_connections=${var.postgres_connections}"] : null
  remove_volumes = true
  stop_timeout   = 10
  ports {
    internal = each.value.port
    external = 0
    ip       = "127.0.0.1"
  }
  volumes {
    volume_name    = docker_volume.data[each.key].name
    container_path = each.value.data
  }
  networks_advanced {
    name    = docker_network.batch.name
    aliases = [each.key]
  }
  dynamic "labels" {
    for_each = merge(local.labels, { "io.infraweave.lab.service" = each.key })
    content {
      label = labels.key
      value = labels.value
    }
  }
  dynamic "upload" {
    for_each = each.key == "gitea" ? { cert = "gitea-cert.pem", key = "gitea-key.pem" } : {}
    content {
      content     = sensitive(file("${var.pki_directory}/${upload.value}"))
      file        = "/data/certs/${upload.key}.pem"
      permissions = "0644"
    }
  }
}

resource "docker_volume" "kubernetes" {
  count = var.kubernetes_enabled ? 1 : 0
  name  = "${var.owner}-kubernetes"
  dynamic "labels" {
    for_each = local.labels
    content {
      label = labels.key
      value = labels.value
    }
  }
}

resource "docker_image" "kubernetes" {
  count        = var.kubernetes_enabled ? 1 : 0
  name         = var.k3s_image
  keep_locally = true
  lifecycle {
    precondition {
      condition     = can(regex("@sha256:[0-9a-f]{64}$", var.k3s_image))
      error_message = "The k3s fixture must use its committed immutable digest."
    }
  }
}

resource "docker_container" "kubernetes" {
  count      = var.kubernetes_enabled ? 1 : 0
  name       = "${var.owner}-kubernetes"
  hostname   = "server"
  image      = docker_image.kubernetes[0].image_id
  privileged = true
  memory     = var.k3s_memory
  cpus       = var.k3s_cpus
  command = [
    "server", "--tls-san=127.0.0.1", "--disable=traefik,servicelb,metrics-server,local-storage,coredns",
    "--disable-helm-controller", "--write-kubeconfig-mode=600", "--node-name=server",
  ]
  tmpfs        = { "/run" = "", "/var/run" = "" }
  stop_timeout = 15
  ports {
    internal = 6443
    external = 0
    ip       = "127.0.0.1"
  }
  volumes {
    volume_name    = docker_volume.kubernetes[0].name
    container_path = "/var/lib/rancher/k3s"
  }
  networks_advanced {
    name    = docker_network.batch.name
    aliases = ["kubernetes"]
  }
  upload {
    file = "/var/lib/rancher/k3s/server/manifests/infraweave-lab.yaml"
    content = yamlencode({
      apiVersion = "v1"
      kind       = "Namespace"
      metadata = {
        name        = local.kubernetes_namespace
        annotations = { "io.infraweave.lab.owner" = var.owner }
      }
    })
    permissions = "0600"
  }
  dynamic "labels" {
    for_each = merge(local.labels, { "io.infraweave.lab.service" = "kubernetes" })
    content {
      label = labels.key
      value = labels.value
    }
  }
}

output "acceptance" {
  value = merge({
    schema = 1
    owner  = var.owner
    batch  = var.batch
    postgres = {
      container = docker_container.service["postgres"].id
      dsn       = "postgres://infraweave:infraweave@127.0.0.1:${docker_container.service["postgres"].ports[0].external}/infraweave?sslmode=disable"
    }
    objectstore = {
      container = docker_container.service["objectstore"].id
      endpoint  = "http://127.0.0.1:${docker_container.service["objectstore"].ports[0].external}"
    }
    gitea = {
      container = docker_container.service["gitea"].id
      url       = "https://127.0.0.1:${docker_container.service["gitea"].ports[0].external}"
      caFile    = "${var.pki_directory}/gitea-ca.pem"
    }
    }, var.kubernetes_enabled ? {
    kubernetes = {
      container  = docker_container.kubernetes[0].id
      endpoint   = "https://127.0.0.1:${docker_container.kubernetes[0].ports[0].external}"
      namespace  = local.kubernetes_namespace
      kubeconfig = "${var.pki_directory}/kubernetes.json"
    }
  } : {})
}
