variable "aws_region" {
  description = "AWS region to deploy into (e.g. us-west-2)."
  type        = string
}

variable "service_name" {
  description = "Short name used to prefix every resource (letters, digits, dashes)."
  type        = string
  default     = "comms-openkit-channels"

  validation {
    condition     = can(regex("^[a-z0-9-]{3,32}$", var.service_name))
    error_message = "service_name must be 3-32 chars, lowercase letters/digits/dashes."
  }
}

variable "container_image" {
  description = <<-EOT
    Fully-qualified container image URI (e.g. 123456789012.dkr.ecr.us-west-2.amazonaws.com/comms-openkit-channels:v0.1.0).
    You are responsible for building and pushing this image to ECR before apply.
    See README for the build/push commands.
  EOT
  type        = string
}

variable "vpc_id" {
  description = "VPC to deploy into. Leave empty ('') to use the account's default VPC."
  type        = string
  default     = ""
}

variable "public_subnet_ids" {
  description = <<-EOT
    Public subnet IDs (>=2, in different AZs) for the ALB and Fargate tasks.
    Leave empty ([]) to auto-discover the default VPC's default subnets.

    For a real production deployment you should give the tasks PRIVATE subnets
    and put the RDS cluster in isolated private subnets. This module keeps
    everything in public subnets so a first-time apply works out of the box.
  EOT
  type        = list(string)
  default     = []
}

variable "db_subnet_ids" {
  description = <<-EOT
    Subnet IDs for the RDS Aurora cluster (>=2 in different AZs).
    Leave empty ([]) to reuse public_subnet_ids — fine for a demo, NOT for prod.
    In production, pass private subnets with no public route.
  EOT
  type        = list(string)
  default     = []
}

variable "certificate_arn" {
  description = "ACM certificate ARN (in the same region) for the HTTPS listener. Required."
  type        = string
}

# ---- app sizing --------------------------------------------------

variable "task_cpu" {
  description = "Fargate task CPU units (512 = 0.5 vCPU)."
  type        = number
  default     = 512
}

variable "task_memory" {
  description = "Fargate task memory in MiB."
  type        = number
  default     = 1024
}

variable "desired_count" {
  description = "Number of Fargate tasks to run."
  type        = number
  default     = 2
}

variable "log_retention_days" {
  description = "CloudWatch Logs retention in days."
  type        = number
  default     = 30
}

# ---- Aurora Postgres --------------------------------------------

variable "db_engine_version" {
  description = "Aurora Postgres engine version. Must support Serverless v2 + PostGIS."
  type        = string
  default     = "16.4"
}

variable "db_master_username" {
  description = "Master DB username. Used to create the initial DB + login user."
  type        = string
  default     = "openkit"
}

variable "db_master_password" {
  description = "Master DB password. If empty (''), a random 32-char password is generated and stored in Secrets Manager as <service_name>-db-password. In a real deploy, source this from your own Secrets Manager secret instead."
  type        = string
  default     = ""
  sensitive   = true
}

variable "db_name" {
  description = "Initial database name."
  type        = string
  default     = "openkit"
}

variable "db_min_capacity" {
  description = "Serverless v2 minimum ACUs (1 ACU ~= 2 GiB memory). 0.5 is the floor."
  type        = number
  default     = 0.5
}

variable "db_max_capacity" {
  description = "Serverless v2 maximum ACUs. Keep low unless you know you need it."
  type        = number
  default     = 2
}

variable "db_backup_retention_days" {
  description = "Automated backup retention in days (1-35)."
  type        = number
  default     = 7
}

# ---- app secrets -------------------------------------------------

variable "internal_api_token_secret_arn" {
  description = "Secrets Manager ARN holding the bearer token shared with the webhook (INTERNAL_API_TOKEN)."
  type        = string
}

variable "platform_secret_arn_map" {
  description = <<-EOT
    Extra Secrets Manager ARNs to inject as env vars for outbound integrations,
    e.g. Twilio API keys, Telegram bot tokens. Any subset. Example:

      {
        TWILIO_ACCOUNT_SID  = "arn:aws:secretsmanager:...:twilio-sid"
        TWILIO_AUTH_TOKEN   = "arn:aws:secretsmanager:...:twilio-token"
      }

    (Per-tenant platform credentials live inside the DB, encrypted by KMS.
    These map entries are for globals only.)
  EOT
  type        = map(string)
  default     = {}
}

variable "extra_env" {
  description = "Plain-text (non-secret) env vars to inject. Do NOT put secrets here."
  type        = map(string)
  default     = {}
}

variable "tags" {
  description = "Common tags applied to every taggable resource."
  type        = map(string)
  default     = {}
}
