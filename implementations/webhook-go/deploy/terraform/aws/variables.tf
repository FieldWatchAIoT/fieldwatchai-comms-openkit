variable "aws_region" {
  description = "AWS region to deploy into (e.g. us-west-2)."
  type        = string
}

variable "service_name" {
  description = "Short name used to prefix every resource (letters, digits, dashes)."
  type        = string
  default     = "comms-openkit-webhook"

  validation {
    condition     = can(regex("^[a-z0-9-]{3,32}$", var.service_name))
    error_message = "service_name must be 3-32 chars, lowercase letters/digits/dashes."
  }
}

variable "container_image" {
  description = <<-EOT
    Fully-qualified container image URI (e.g. 123456789012.dkr.ecr.us-west-2.amazonaws.com/comms-openkit-webhook:v0.1.0).
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
  EOT
  type        = list(string)
  default     = []
}

variable "certificate_arn" {
  description = "ACM certificate ARN (in the same region) for the HTTPS listener. Required."
  type        = string
}

variable "task_cpu" {
  description = "Fargate task CPU units (256 = 0.25 vCPU)."
  type        = number
  default     = 256
}

variable "task_memory" {
  description = "Fargate task memory in MiB."
  type        = number
  default     = 512
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

variable "channels_url" {
  description = "URL of the downstream channels service the webhook forwards to (e.g. https://channels.example.com)."
  type        = string
}

variable "internal_api_token_secret_arn" {
  description = "Secrets Manager ARN holding the bearer token used to authenticate to the channels service (INTERNAL_API_TOKEN)."
  type        = string
}

variable "platform_secret_arn_map" {
  description = <<-EOT
    Map of platform-listener secrets to inject as env vars. Any subset. Example:

      {
        WHATSAPP_ULTRAMSG_WEBHOOK_SECRET = "arn:aws:secretsmanager:...:ultramsg"
        TWILIO_AUTH_TOKEN                = "arn:aws:secretsmanager:...:twilio"
        TELEGRAM_WEBHOOK_SECRET          = "arn:aws:secretsmanager:...:telegram"
        EMAIL_SES_WEBHOOK_SECRET         = "arn:aws:secretsmanager:...:ses"
      }
  EOT
  type        = map(string)
  default     = {}
}

variable "extra_env" {
  description = <<-EOT
    Plain-text (non-secret) env vars to inject, e.g. PUBLIC_BASE_URL, EMAIL_SES_TOPIC_ARN, ACCOUNTS_MAP.
    Do NOT put secrets here — use platform_secret_arn_map.
  EOT
  type        = map(string)
  default     = {}
}

variable "sqs_max_receive_count" {
  description = "SQS redrive: after this many receives without a delete, a message goes to the DLQ."
  type        = number
  default     = 5
}

variable "tags" {
  description = "Common tags applied to every taggable resource."
  type        = map(string)
  default     = {}
}
