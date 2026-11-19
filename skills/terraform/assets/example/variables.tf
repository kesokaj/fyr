variable "project" {
  description = "GCP project ID (DEPLOY-gcp)"
  type        = string
}

variable "region" {
  description = "Region for Artifact Registry and Cloud Run"
  type        = string
}

variable "app" {
  description = "Service name; also the Cloud Run service and the secret prefix"
  type        = string
}

variable "repo" {
  description = "Artifact Registry repository ID"
  type        = string
}

variable "git_sha" {
  description = "Image tag: the git short SHA cloud-run-deploy built and pushed"
  type        = string
}

variable "app_secret" {
  description = "The one secret the service reads as APP_SECRET; write-only, never in state"
  type        = string
  sensitive   = true
  ephemeral   = true
}

variable "public" {
  description = "true disables the invoker IAM check (public service); false keeps it IAM-gated"
  type        = bool
  default     = false
}
