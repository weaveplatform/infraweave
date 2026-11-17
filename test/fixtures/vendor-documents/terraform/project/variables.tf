variable "api_key" {
  type      = string
  sensitive = true
}

variable "session_token" {
  type      = string
  ephemeral = true
}
