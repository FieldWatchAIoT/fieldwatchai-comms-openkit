####################################################################
# comms-openkit channels — Aurora Postgres + ECS Fargate + ALB + KMS
####################################################################
#
# This is a REFERENCE module. It stands the channels service up on AWS with
# enough real infra (Aurora Serverless v2, a real KMS key, a real IAM boundary)
# to be useful, but it is intentionally small enough to read in one sitting.
# Review every resource and adapt it to your org's networking, IAM,
# encryption, and logging policies before applying.
#
# What this creates:
#   - CloudWatch log group (30d retention by default)
#   - KMS customer-managed key (aliased) for encrypting per-account platform
#     credentials at rest (used by the app's KMS Encryptor)
#   - RDS Aurora Postgres cluster (Serverless v2) + one writer instance,
#     encrypted at rest with the customer-managed KMS key
#   - Secrets Manager entries for the DB password (generated if not supplied)
#   - ECS cluster (Fargate) + task definition + service
#   - ALB (internet-facing) + HTTPS listener + HTTP -> HTTPS redirect
#   - Security groups: ALB SG, task SG, DB SG (task-only ingress on 5432)
#   - IAM: task-execution role (pulls image, reads secrets, writes logs) +
#     task role (KMS Decrypt / GenerateDataKey on the customer-managed key)

provider "aws" {
  region = var.aws_region
}

locals {
  name = var.service_name
  tags = merge({
    Service   = "comms-openkit-channels"
    ManagedBy = "terraform"
  }, var.tags)
}

# ---------- VPC / subnets ------------------------------------------

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
  vpc_id         = var.vpc_id == "" ? data.aws_vpc.default[0].id : var.vpc_id
  app_subnet_ids = length(var.public_subnet_ids) == 0 ? data.aws_subnets.default[0].ids : var.public_subnet_ids
  db_subnet_ids  = length(var.db_subnet_ids) == 0 ? local.app_subnet_ids : var.db_subnet_ids
}

# ---------- CloudWatch logs ---------------------------------------

resource "aws_cloudwatch_log_group" "app" {
  name              = "/ecs/${local.name}"
  retention_in_days = var.log_retention_days
  tags              = local.tags
}

# ---------- KMS for credential encryption -------------------------

# The customer-managed key the app's KMS Encryptor uses to protect per-account
# platform API credentials in the accounts.credentials_encrypted column.
resource "aws_kms_key" "credentials" {
  description             = "${local.name} — encrypts per-account platform credentials"
  deletion_window_in_days = 30
  enable_key_rotation     = true
  tags                    = local.tags
}

resource "aws_kms_alias" "credentials" {
  name          = "alias/${local.name}-credentials"
  target_key_id = aws_kms_key.credentials.key_id
}

# ---------- DB password (generated if not supplied) --------------

resource "random_password" "db" {
  count            = var.db_master_password == "" ? 1 : 0
  length           = 32
  special          = true
  override_special = "!#$%&*+-.:;<=>?@^_"
}

locals {
  db_password_effective = var.db_master_password != "" ? var.db_master_password : random_password.db[0].result
}

resource "aws_secretsmanager_secret" "db_password" {
  name        = "${local.name}-db-password"
  description = "Master DB password for the ${local.name} Aurora cluster"
  tags        = local.tags
}

resource "aws_secretsmanager_secret_version" "db_password" {
  secret_id     = aws_secretsmanager_secret.db_password.id
  secret_string = local.db_password_effective
}

# ---------- Security groups ---------------------------------------

resource "aws_security_group" "alb" {
  name        = "${local.name}-alb"
  description = "ingress 80/443 from anywhere for the channels ALB"
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
  description = "channels Fargate task — accepts only from ALB SG"
  vpc_id      = local.vpc_id
  tags        = local.tags
}

resource "aws_security_group_rule" "task_ingress_from_alb" {
  security_group_id        = aws_security_group.task.id
  type                     = "ingress"
  from_port                = 9090
  to_port                  = 9090
  protocol                 = "tcp"
  source_security_group_id = aws_security_group.alb.id
  description              = "channels port (only ALB may connect)"
}

resource "aws_security_group_rule" "task_egress_all" {
  security_group_id = aws_security_group.task.id
  type              = "egress"
  from_port         = 0
  to_port           = 0
  protocol          = "-1"
  cidr_blocks       = ["0.0.0.0/0"]
}

resource "aws_security_group" "db" {
  name        = "${local.name}-db"
  description = "Aurora Postgres — accepts 5432 only from the task SG"
  vpc_id      = local.vpc_id
  tags        = local.tags
}

resource "aws_security_group_rule" "db_ingress_from_task" {
  security_group_id        = aws_security_group.db.id
  type                     = "ingress"
  from_port                = 5432
  to_port                  = 5432
  protocol                 = "tcp"
  source_security_group_id = aws_security_group.task.id
  description              = "postgres from ECS task SG only"
}

# ---------- RDS Aurora Postgres (Serverless v2) ------------------

resource "aws_db_subnet_group" "db" {
  name       = local.name
  subnet_ids = local.db_subnet_ids
  tags       = local.tags
}

resource "aws_rds_cluster" "db" {
  cluster_identifier      = local.name
  engine                  = "aurora-postgresql"
  engine_mode             = "provisioned"
  engine_version          = var.db_engine_version
  database_name           = var.db_name
  master_username         = var.db_master_username
  master_password         = local.db_password_effective
  db_subnet_group_name    = aws_db_subnet_group.db.name
  vpc_security_group_ids  = [aws_security_group.db.id]
  storage_encrypted       = true
  kms_key_id              = aws_kms_key.credentials.arn
  backup_retention_period = var.db_backup_retention_days
  preferred_backup_window = "03:00-04:00"
  skip_final_snapshot     = true
  apply_immediately       = true
  # Serverless v2 uses the provisioned engine_mode + this scaling block.
  serverlessv2_scaling_configuration {
    min_capacity = var.db_min_capacity
    max_capacity = var.db_max_capacity
  }
  tags = local.tags
}

resource "aws_rds_cluster_instance" "writer" {
  identifier          = "${local.name}-writer"
  cluster_identifier  = aws_rds_cluster.db.id
  instance_class      = "db.serverless"
  engine              = aws_rds_cluster.db.engine
  engine_version      = aws_rds_cluster.db.engine_version
  publicly_accessible = false
  tags                = local.tags
}

# ---------- ALB + TG + listeners ----------------------------------

resource "aws_lb" "app" {
  name               = local.name
  internal           = false
  load_balancer_type = "application"
  security_groups    = [aws_security_group.alb.id]
  subnets            = local.app_subnet_ids
  tags               = local.tags
}

resource "aws_lb_target_group" "app" {
  name        = "${local.name}-tg"
  port        = 9090
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

# ---------- IAM ---------------------------------------------------

data "aws_iam_policy_document" "assume_ecs_tasks" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
  }
}

# Task-execution role: pulls image + reads secrets + writes logs.
resource "aws_iam_role" "task_execution" {
  name               = "${local.name}-task-execution"
  assume_role_policy = data.aws_iam_policy_document.assume_ecs_tasks.json
  tags               = local.tags
}

resource "aws_iam_role_policy_attachment" "task_execution_managed" {
  role       = aws_iam_role.task_execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

locals {
  all_secret_arns = concat(
    [
      var.internal_api_token_secret_arn,
      aws_secretsmanager_secret.db_password.arn,
    ],
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

# Task role: what the container itself may do. It calls KMS for per-account
# credential encryption/decryption. It does NOT need Secrets Manager at
# runtime (Fargate resolves those into env vars at task start).
resource "aws_iam_role" "task" {
  name               = "${local.name}-task"
  assume_role_policy = data.aws_iam_policy_document.assume_ecs_tasks.json
  tags               = local.tags
}

data "aws_iam_policy_document" "task_kms" {
  statement {
    actions = [
      "kms:Encrypt",
      "kms:Decrypt",
      "kms:GenerateDataKey",
      "kms:DescribeKey",
    ]
    resources = [aws_kms_key.credentials.arn]
  }
}

resource "aws_iam_role_policy" "task_kms" {
  name   = "${local.name}-kms"
  role   = aws_iam_role.task.id
  policy = data.aws_iam_policy_document.task_kms.json
}

# SES sending — the email integration authenticates via the task role.
data "aws_iam_policy_document" "task_ses" {
  statement {
    actions   = ["ses:SendEmail", "ses:SendRawEmail"]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "task_ses" {
  name   = "${local.name}-ses"
  role   = aws_iam_role.task.id
  policy = data.aws_iam_policy_document.task_ses.json
}

# ---------- ECS cluster + task def + service ----------------------

resource "aws_ecs_cluster" "app" {
  name = local.name
  tags = local.tags

  setting {
    name  = "containerInsights"
    value = "enabled"
  }
}

locals {
  # DB_PASSWORD is injected via `secrets`, so we build DATABASE_URL at boot
  # instead of hard-coding the password in `environment`. The app expects the
  # full DSN in DATABASE_URL — teams that want the assembled-at-boot pattern
  # should wrap the ENTRYPOINT or use a small init script.
  db_host = aws_rds_cluster.db.endpoint

  base_env = merge(
    {
      ENV                    = "prod"
      PORT                   = "9090"
      LOG_LEVEL              = "info"
      AWS_REGION             = var.aws_region
      CREDENTIALS_ENCRYPTION = "kms"
      CREDENTIALS_KMS_KEY_ID = aws_kms_key.credentials.arn
      # NOTE: DATABASE_URL is composed from the DB endpoint + the secret
      # DB_PASSWORD injected below. If your image supports env expansion you
      # can set it here; otherwise set it in your task entrypoint / a small
      # config-composer wrapper. Documented as-is so operators see the shape.
      DB_HOST     = local.db_host
      DB_PORT     = "5432"
      DB_NAME     = var.db_name
      DB_USERNAME = var.db_master_username
    },
    var.extra_env,
  )

  base_secrets = merge(
    {
      INTERNAL_API_TOKEN = var.internal_api_token_secret_arn
      DB_PASSWORD        = aws_secretsmanager_secret.db_password.arn
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
    name         = "channels"
    image        = var.container_image
    essential    = true
    portMappings = [{ containerPort = 9090, protocol = "tcp" }]
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
    subnets          = local.app_subnet_ids
    security_groups  = [aws_security_group.task.id]
    assign_public_ip = true # required when using the default VPC's public subnets
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.app.arn
    container_name   = "channels"
    container_port   = 9090
  }

  deployment_minimum_healthy_percent = 50
  deployment_maximum_percent         = 200

  depends_on = [
    aws_lb_listener.https,
    aws_iam_role_policy.task_execution_secrets,
    aws_rds_cluster_instance.writer,
  ]

  tags = local.tags
}
