output "alb_dns_name" {
  description = "DNS name of the internet-facing ALB. Point your channels-service CNAME/ALIAS at this."
  value       = aws_lb.app.dns_name
}

output "channels_url" {
  description = "Base URL of the deployed channels service (HTTPS)."
  value       = "https://${aws_lb.app.dns_name}"
}

output "db_endpoint" {
  description = "Aurora cluster writer endpoint. Use in DATABASE_URL / DB_HOST."
  value       = aws_rds_cluster.db.endpoint
}

output "db_reader_endpoint" {
  description = "Aurora cluster reader endpoint (single-writer cluster today, but the reader endpoint routes to the writer)."
  value       = aws_rds_cluster.db.reader_endpoint
}

output "db_password_secret_arn" {
  description = "Secrets Manager ARN holding the master DB password. Fargate injects this as DB_PASSWORD."
  value       = aws_secretsmanager_secret.db_password.arn
}

output "kms_key_arn" {
  description = "ARN of the customer-managed KMS key used to encrypt per-account platform credentials."
  value       = aws_kms_key.credentials.arn
}

output "kms_key_alias" {
  description = "Alias for the customer-managed KMS key. Set CREDENTIALS_KMS_KEY_ID to this or the ARN."
  value       = aws_kms_alias.credentials.name
}

output "log_group_name" {
  description = "CloudWatch log group holding container stdout/stderr."
  value       = aws_cloudwatch_log_group.app.name
}

output "ecs_cluster_name" {
  description = "ECS cluster name."
  value       = aws_ecs_cluster.app.name
}

output "ecs_service_name" {
  description = "ECS service name."
  value       = aws_ecs_service.app.name
}
