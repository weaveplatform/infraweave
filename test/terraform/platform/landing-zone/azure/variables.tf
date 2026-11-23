variable "subscription_id" {
  description = "An existing subscription the provider signs in through. The lab subscription is created at billing_scope_id, not inside it."
  type        = string
}

variable "billing_scope_id" {
  description = "The billing scope the lab subscription is billed to: an invoice section of a Microsoft Customer Agreement (/providers/Microsoft.Billing/billingAccounts/<account>/billingProfiles/<profile>/invoiceSections/<section>), an Enterprise Agreement enrollment account (/providers/Microsoft.Billing/billingAccounts/<account>/enrollmentAccounts/<enrollment>), or a customer of a Microsoft Partner Agreement."
  type        = string

  validation {
    condition     = startswith(var.billing_scope_id, "/providers/Microsoft.Billing/billingAccounts/")
    error_message = "billing_scope_id must be a billing scope under /providers/Microsoft.Billing/billingAccounts/."
  }
}

variable "subscription_name" {
  description = "The lab subscription's display name, which is also its alias."
  type        = string
  default     = "infraweave-lab"
}

variable "workload" {
  description = "The subscription's workload type. DevTest needs a Dev/Test offer on the billing account."
  type        = string
  default     = "Production"

  validation {
    condition     = contains(["Production", "DevTest"], var.workload)
    error_message = "workload must be Production or DevTest."
  }
}

variable "cancel_on_destroy" {
  description = "Whether destroying the root cancels the subscription. Without this a destroy removes only the alias, and the subscription stays."
  type        = bool
  default     = false
}
