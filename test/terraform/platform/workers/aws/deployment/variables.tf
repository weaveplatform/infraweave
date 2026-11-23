variable "ownership" {
  description = "Supplied by the lab coordinator: owner, batch, RFC 3339 expires_at and the io.infraweave.lab.* labels."
  type = object({
    owner      = string
    batch      = string
    expires_at = string
    labels     = map(string)
  })
}

variable "cloud" {
  description = "The explicit AWS account and region of the cluster."
  type = object({
    provider    = string
    identifiers = object({ account_id = string, region = string })
  })

  validation {
    condition     = var.cloud.provider == "aws"
    error_message = "cloud.provider must be aws."
  }
}

variable "cluster_stage" {
  description = "Supplied by the lab coordinator: the cluster stage's validated cluster output."
  type = object({
    schema         = number
    kind           = string
    owner          = string
    batch          = string
    provider       = string
    id             = string
    name           = string
    endpoint       = string
    ca_certificate = string
    owned          = bool
    inventory = list(object({
      kind      = string
      id        = string
      scope     = string
      owned     = bool
      owner     = string
      lifecycle = string
    }))
  })
}

variable "cluster" {
  description = "The cluster the deployment expects: its name, mode and EKS minor, and cluster_id, the installation-assigned DNS label every pool's owner carries."
  type = object({
    mode               = string
    name               = string
    cluster_id         = string
    kubernetes_version = string
    existing_id        = optional(string)
  })

  validation {
    condition     = can(regex("^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$", var.cluster.cluster_id))
    error_message = "cluster.cluster_id must be a DNS label of at most 63 characters."
  }
}

variable "namespace" {
  description = "The namespace the release, its controller and its pools live in. It must already exist: setup_owned says whether the lab's setup created it (and deletes it after this stage is destroyed) or it is delegated and survives."
  type = object({
    name        = string
    setup_owned = bool
  })

  validation {
    condition     = can(regex("^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$", var.namespace.name)) && !startswith(var.namespace.name, "kube-") && var.namespace.name != "default"
    error_message = "namespace.name must be a DNS label that is not a system namespace."
  }
}

variable "installation" {
  description = "The Helm release name, and whether this release owns the workers.infraweave.io CRDs (owned) or a cluster administrator already installed compatible ones (existing)."
  type = object({
    release_name = string
    crds         = string
  })

  validation {
    condition     = contains(["owned", "existing"], var.installation.crds)
    error_message = "installation.crds must be owned or existing."
  }
}

variable "artifacts" {
  description = "The release's worker-artifacts.json, as published by worker-artifacts.yml: the chart's OCI source, version and manifest digest, and the controller, bootstrap and runner images pinned by digest."
  type = object({
    revision         = string
    controller_image = string
    bootstrap_image  = string
    runner_images    = map(string)
    chart = object({
      source  = string
      version = string
      digest  = string
      sha256  = string
    })
  })

  validation {
    condition = alltrue([
      for image in concat([var.artifacts.controller_image, var.artifacts.bootstrap_image], values(var.artifacts.runner_images)) :
      can(regex("^[^@\\s]+@sha256:[0-9a-f]{64}$", image))
    ])
    error_message = "Every artifact image must be pinned as <repository>@sha256:<digest>."
  }
  validation {
    condition     = can(regex("^oci://", var.artifacts.chart.source)) && can(regex("^sha256:[0-9a-f]{64}$", var.artifacts.chart.digest)) && can(regex("^(sha256:)?[0-9a-f]{64}$", var.artifacts.chart.sha256))
    error_message = "artifacts.chart needs an oci:// source and sha256 digests."
  }
}

variable "module_revision" {
  description = "The exact commit of this module's checkout, recorded in the hosting output."
  type        = string

  validation {
    condition     = can(regex("^[0-9a-f]{40}$", var.module_revision))
    error_message = "module_revision must be a full 40-character commit."
  }
}

variable "server" {
  description = "The infraweave server the pools register with: a bare HTTPS origin, trusted through the system roots or the PEM bundle ca_secret_ref names in the namespace. TLS verification is never off."
  type = object({
    endpoint = string
    tls = object({
      system_roots  = bool
      ca_secret_ref = optional(object({ name = string, key = string }))
    })
  })

  validation {
    condition     = can(regex("^https://[^/?#@ ]+/?$", var.server.endpoint))
    error_message = "server.endpoint must be a bare HTTPS origin without credentials, path or query."
  }
  validation {
    condition     = var.server.tls.system_roots || var.server.tls.ca_secret_ref != null
    error_message = "server.tls without system_roots needs ca_secret_ref."
  }
}

variable "management_credentials_ref" {
  description = "The Secret in the namespace holding the worker-pool-controller API key's ID and secret under id_key and secret_key. Required when a pool is Managed, forbidden otherwise."
  type = object({
    name       = string
    id_key     = string
    secret_key = string
  })
  default = null
}

variable "pools" {
  description = "The WorkerPools the release owns. capacity is logical run slots. Existing and Public pools name their pool_id; Managed pools learn it from status. credentials_ref is the Secret holding privateKey (and token, for Existing and Public). settings is the rest of the WorkerPool spec: name, description, labels, environment, mounts and the like."
  type = list(object({
    resource_name   = string
    mode            = string
    pool_id         = optional(string)
    account         = optional(string)
    space           = optional(string)
    capacity        = number
    credentials_ref = string
    settings        = optional(any, {})
  }))

  validation {
    condition     = length(var.pools) > 0 && length(distinct([for p in var.pools : p.resource_name])) == length(var.pools)
    error_message = "pools must list at least one pool, with distinct resource names."
  }
  validation {
    condition = alltrue([for p in var.pools :
      contains(["Managed", "Existing", "Public"], p.mode) &&
      (p.mode == "Managed") == (p.pool_id == null) &&
      (p.mode != "Public" || (p.account == null && p.space == null)) &&
      p.capacity >= 0 && p.capacity == floor(p.capacity)
    ])
    error_message = "Each pool is Managed without pool_id, or Existing or Public with one; Public pools take no account or space; capacity is a whole number."
  }
}

variable "resources" {
  description = "Requests and limits of the controller, each run Pod and its policy helper, the run Pod's Pod-local storage and the controller's staging cache, and the scheduling subset the chart admits."
  type = object({
    controller = object({
      requests = map(string)
      limits   = map(string)
    })
    runner = object({
      requests = map(string)
      limits   = map(string)
    })
    policy_helper = optional(object({
      requests = map(string)
      limits   = map(string)
    }))
    storage = object({
      run_size         = string
      controller_cache = string
    })
    scheduling = optional(object({
      node_selector = optional(map(string), {})
    }), {})
  })
}

variable "timeouts" {
  description = "How long the install may wait for the controller to be ready."
  type = object({
    readiness_seconds = number
  })
  default = { readiness_seconds = 900 }
}
