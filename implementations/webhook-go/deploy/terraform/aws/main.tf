####################################################################
# comms-openkit webhook — ECS Fargate + ALB + SQS reference module
####################################################################
#
# This is a REFERENCE module. It is deliberately small and readable, not a
# production-hardened landing zone. Review every resource before applying and
# adapt it to your org's networking, encryption, and IAM policies.
#
# What this creates:
#   - CloudWatch log group (30d retention by default)
#   - SQS main queue + dead-letter queue with a redrive policy
#   - ECS cluster (Fargate) + task definition + service
#   - ALB (internet-facing) + HTTPS listener + HTTP -> HTTPS redirect
#   - Security groups (ALB SG, task SG) that only allow ALB -> task
#   - IAM: task execution role (pulls image + reads referenced secrets) and
#     task role (SendMessage/Receive on the SQS queue only)

provider "aws" {
  region = var.aws_region
}

locals {
  name = var.service_name
  tags = merge({
    Service   = "comms-openkit-webhook"
    ManagedBy = "terraform"
  }, var.tags)
}

# ---------- VPC / subnets ------------------------------------------

# When vpc_id is empty, fall back to the account's default VPC + default subnets
# so `terraform apply` works out of the box. In real deployments, pass your own.
data "aws_vpc" "default" {
  count   = var.vpc_id == "" ? 1 : 0
  default = true
}

data "aws_subnets" "default" {
  count = length(var.public_subnet_ids) == 0 ? 1 : 0
  filter {
    name   = "vpc-id"
    values = [var.vpc_id == "" ? data.aws_vpc.default[0].id : var.vpc_id]
  }
}

locals {
  vpc_id     = var.vpc_id == "" ? data.aws_vpc.default[0].id : var.vpc_id
  subnet_ids = length(var.public_subnet_ids) == 0 ? data.aws_subnets.default[0].ids : var.public_subnet_ids
}

# ---------- Logs ---------------------------------------------------

resource "aws_cloudwatch_log_group" "app" {
  name              = "/ecs/${local.name}"
  retention_in_days = var.log_retention_days
  tags              = local.tags
}

# ---------- SQS: main + DLQ ----------------------------------------

resource "aws_sqs_queue" "dlq" {
  name                      = "${local.name}-dlq"
  message_retention_seconds = 1209600 # 14 days (SQS max)
  tags                      = local.tags
}

resource "aws_sqs_queue" "main" {
  name                       = local.name
  visibility_timeout_seconds = 60
  message_retention_seconds  = 345600 # 4 days
  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.dlq.arn
    maxReceiveCount     = var.sqs_max_receive_count
  })
  tags = local.tags
}

# ---------- Security groups ----------------------------------------

resource "aws_security_group" "alb" {
  name        = "${local.name}-alb"
  description = "ingress 80/443 from anywhere for the webhook ALB"
  vpc_id      = local.vpc_id
  tags        = local.tags
}

resource "aws_security_group_rule" "alb_ingress_http" {
  security_group_id = aws_security_group.alb.id
  type              = "ingress"
  from_port         = 80
  to_port           = 80
  protocol          = "tcp"
  cidr_blocks       = ["0.0.0.0/0"]
  description       = "http (redirected to https)"
}

resource "aws_security_group_rule" "alb_ingress_https" {
  security_group_id = aws_security_group.alb.id
  type              = "ingress"
  from_port         = 443
  to_port           = 443
  protocol          = "tcp"
  cidr_blocks       = ["0.0.0.0/0"]
  description       = "https"
}

resource "aws_security_group_rule" "alb_egress_all" {
  security_group_id = aws_security_group.alb.id
  type              = "egress"
  from_port         = 0
  to_port           = 0
  protocol          = "-1"
  cidr_blocks       = ["0.0.0.0/0"]
}

resource "aws_security_group" "task" {
  name        = "${local.name}-task"
  description = "webhook Fargate task — accepts only from ALB SG"
  vpc_id      = local.vpc_id
  tags        = local.tags
}

resource "aws_security_group_rule" "task_ingress_from_alb" {
  security_group_id        = aws_security_group.task.id
  type                     = "ingress"
  from_port                = 8080
  to_port                  = 8080
  protocol                 = "tcp"
  source_security_group_id = aws_security_group.alb.id
  description              = "webhook port (only ALB may connect)"
}

resource "aws_security_group_rule" "task_egress_all" {
  security_group_id = aws_security_group.task.id
  type              = "egress"
  from_port         = 0
  to_port           = 0
  protocol          = "-1"
  cidr_blocks       = ["0.0.0.0/0"]
}

# ---------- ALB + TG + listeners -----------------------------------

resource "aws_lb" "app" {
  name               = local.name
  internal           = false
  load_balancer_type = "application"
  security_groups    = [aws_security_group.alb.id]
  subnets            = local.subnet_ids
  tags               = local.tags
}

resource "aws_lb_target_group" "app" {
  name        = "${local.name}-tg"
  port        = 8080
  protocol    = "HTTP"
  target_type = "ip"
  vpc_id      = local.vpc_id

  health_check {
    path                = "/healthz"
    healthy_threshold   = 2
    unhealthy_threshold = 3
    interval            = 15
    timeout             = 5
    matcher             = "200"
  }

  tags = local.tags
}

resource "aws_lb_listener" "http_redirect" {
  load_balancer_arn = aws_lb.app.arn
  port              = 80
  protocol          = "HTTP"

  default_action {
    type = "redirect"
    redirect {
      port        = "443"
      protocol    = "HTTPS"
      status_code = "HTTP_301"
    }
  }
}

resource "aws_lb_listener" "https" {
  load_balancer_arn = aws_lb.app.arn
  port              = 443
  protocol          = "HTTPS"
  ssl_policy        = "ELBSecurityPolicy-TLS13-1-2-2021-06"
  certificate_arn   = var.certificate_arn

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.app.arn
  }
}

# ---------- IAM ----------------------------------------------------

# Task-execution role: pulls image + reads secrets and writes logs.
data "aws_iam_policy_document" "assume_ecs_tasks" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "task_execution" {
  name               = "${local.name}-task-execution"
  assume_role_policy = data.aws_iam_policy_document.assume_ecs_tasks.json
  tags               = local.tags
}

resource "aws_iam_role_policy_attachment" "task_execution_managed" {
  role       = aws_iam_role.task_execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

# Grant the execution role read on every configured secret ARN (used by the
# task definition's `secrets` block to inject env vars at container start).
locals {
  all_secret_arns = concat(
    [var.internal_api_token_secret_arn],
    values(var.platform_secret_arn_map),
  )
}

data "aws_iam_policy_document" "task_execution_secrets" {
  statement {
    actions   = ["secretsmanager:GetSecretValue"]
    resources = local.all_secret_arns
  }
}

resource "aws_iam_role_policy" "task_execution_secrets" {
  name   = "${local.name}-secrets-read"
  role   = aws_iam_role.task_execution.id
  policy = data.aws_iam_policy_document.task_execution_secrets.json
}

# Task role: what the running container itself may do. Currently only SQS.
resource "aws_iam_role" "task" {
  name               = "${local.name}-task"
  assume_role_policy = data.aws_iam_policy_document.assume_ecs_tasks.json
  tags               = local.tags
}

data "aws_iam_policy_document" "task_sqs" {
  statement {
    actions = [
      "sqs:SendMessage",
      "sqs:ReceiveMessage",
      "sqs:DeleteMessage",
      "sqs:ChangeMessageVisibility",
      "sqs:GetQueueAttributes",
      "sqs:GetQueueUrl",
    ]
    resources = [
      aws_sqs_queue.main.arn,
      aws_sqs_queue.dlq.arn,
    ]
  }
}

resource "aws_iam_role_policy" "task_sqs" {
  name   = "${local.name}-sqs"
  role   = aws_iam_role.task.id
  policy = data.aws_iam_policy_document.task_sqs.json
}

# ---------- ECS cluster + task def + service -----------------------

resource "aws_ecs_cluster" "app" {
  name = local.name
  tags = local.tags

  setting {
    name  = "containerInsights"
    value = "enabled"
  }
}

# Build the container definition. `environment` gets the plain vars; `secrets`
# gets the Secrets Manager references — Fargate resolves them to env vars at
# container start, so the app never has to call Secrets Manager itself.
locals {
  base_env = merge(
    {
      ENV           = "prod"
      PORT          = "8080"
      AWS_REGION    = var.aws_region
      CHANNELS_URL  = var.channels_url
      SQS_QUEUE_URL = aws_sqs_queue.main.url
      SQS_DLQ_URL   = aws_sqs_queue.dlq.url
    },
    var.extra_env,
  )

  base_secrets = merge(
    {
      INTERNAL_API_TOKEN = var.internal_api_token_secret_arn
    },
    var.platform_secret_arn_map,
  )

  container_env = [
    for k, v in local.base_env : { name = k, value = v }
  ]

  container_secrets = [
    for k, v in local.base_secrets : { name = k, valueFrom = v }
  ]

  container_def = [{
    name         = "webhook"
    image        = var.container_image
    essential    = true
    portMappings = [{ containerPort = 8080, protocol = "tcp" }]
    environment  = local.container_env
    secrets      = local.container_secrets
    logConfiguration = {
      logDriver = "awslogs"
      options = {
        awslogs-group         = aws_cloudwatch_log_group.app.name
        awslogs-region        = var.aws_region
        awslogs-stream-prefix = "ecs"
      }
    }
  }]
}

resource "aws_ecs_task_definition" "app" {
  family                   = local.name
  cpu                      = tostring(var.task_cpu)
  memory                   = tostring(var.task_memory)
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  execution_role_arn       = aws_iam_role.task_execution.arn
  task_role_arn            = aws_iam_role.task.arn
  container_definitions    = jsonencode(local.container_def)
  tags                     = local.tags
}

resource "aws_ecs_service" "app" {
  name            = local.name
  cluster         = aws_ecs_cluster.app.id
  task_definition = aws_ecs_task_definition.app.arn
  desired_count   = var.desired_count
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = local.subnet_ids
    security_groups  = [aws_security_group.task.id]
    assign_public_ip = true # required when using the default VPC's public subnets
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.app.arn
    container_name   = "webhook"
    container_port   = 8080
  }

  deployment_minimum_healthy_percent = 50
  deployment_maximum_percent         = 200

  depends_on = [
    aws_lb_listener.https,
    aws_iam_role_policy.task_execution_secrets,
  ]

  tags = local.tags
}
