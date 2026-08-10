output "alb_dns_name" {
  description = "DNS name of the internet-facing ALB. Point your custom-domain CNAME/ALIAS at this."
  value       = aws_lb.app.dns_name
}

output "webhook_url" {
  description = "Base URL of the deployed webhook (HTTPS). Configure your platform webhooks under this origin."
  value       = "https://${aws_lb.app.dns_name}"
}

output "sqs_queue_url" {
  description = "Main SQS queue URL used by the webhook for its durable buffer."
  value       = aws_sqs_queue.main.url
}

output "sqs_dlq_url" {
  description = "Dead-letter queue URL. Alarm on messages here — they represent messages the app couldn't process after N receives."
  value       = aws_sqs_queue.dlq.url
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
