# AWS reference deployment — Aurora Postgres + ECS Fargate + ALB + KMS

Minimal, readable Terraform for standing the comms-openkit **channels**
service up on AWS. This is the stateful half of the openkit — it owns the
database — so the module provisions real RDS Aurora, a real customer-managed
KMS key, and Secrets Manager entries alongside the ECS + ALB pattern the
webhook module uses.

> **Reference module, not a production template.** Read every resource before
> applying and adapt it to your org's networking, IAM, encryption, and
> logging policies. The **DB config in particular** — engine version, ACU
> ceiling, backup retention, deletion protection, snapshotting on destroy —
> is set for a quick reference apply, not for a real production database.
> Set `skip_final_snapshot = false` and turn on `deletion_protection` before
> you put anything you care about in this cluster.

## What this creates

| Resource | Purpose |
|---|---|
| ECS cluster (Fargate) | runs the channels containers |
| ECS task definition + service | 2 tasks (0.5 vCPU / 1 GB by default), `awslogs` |
| ALB (internet-facing) | HTTPS listener + HTTP -> HTTPS redirect |
| Target group | `/healthz` health check |
| KMS customer-managed key | encrypts per-account platform credentials at rest (used by the app's KMS Encryptor) |
| RDS Aurora Postgres cluster (Serverless v2) | the service's own database — storage encrypted with the KMS key |
| RDS Aurora writer instance (`db.serverless`) | writer + reader endpoints |
| Secrets Manager secret | master DB password (auto-generated if not supplied) |
| Security groups | ALB accepts 80/443 from anywhere; tasks accept 9090 only from the ALB SG; DB accepts 5432 only from the task SG |
| IAM task-execution role | pulls image, reads referenced Secrets Manager ARNs, writes logs |
| IAM task role | KMS Encrypt/Decrypt/GenerateDataKey on the CMK; SES SendEmail for the email integration |
| CloudWatch log group | 30-day retention (configurable) |

## Rough cost estimate

At low traffic (a couple of hundred messages/day, single writer, no reader
replica):

| | Monthly |
|---|---|
| ALB (idle + LCUs) | ~$18 |
| 2 Fargate tasks (0.5 vCPU / 1 GB, 24x7) | ~$20 |
| Aurora Serverless v2 (0.5 ACU floor, ~2 ACU peak) | ~$45 - $90 |
| Aurora storage (~5 GB) | ~$0.50 |
| KMS CMK | ~$1 + $0.03/10k requests |
| Secrets Manager (1-2 secrets) | ~$1 |
| CloudWatch Logs (30d, low volume) | ~$1 |
| **Total** | **~$100 - $150 / mo** |

Aurora is the dominant cost. Prices depend on region and traffic. Verify with
the AWS pricing calculator before deploying anything you care about. If you
just want the app up briefly to try it, `terraform destroy` when done — see
tear-down below.

## Prerequisites

- Terraform >= 1.6, AWS provider ~> 5.0
- AWS CLI configured (`aws sts get-caller-identity` works)
- An ACM certificate in the same region for whatever hostname you'll front
  the ALB with
- A Secrets Manager secret holding your `INTERNAL_API_TOKEN` (this must
  MATCH the value the webhook uses, so `/v1/ingest` calls authenticate)
- An ECR repository, and the channels image built + pushed into it

## Build + push the container image

From the implementation root (`implementations/channels-go/`):

```sh
# 1. create an ECR repo (once)
aws ecr create-repository --repository-name comms-openkit-channels --region us-west-2

# 2. auth docker to ECR
aws ecr get-login-password --region us-west-2 \
  | docker login --username AWS --password-stdin \
      "$(aws sts get-caller-identity --query Account --output text).dkr.ecr.us-west-2.amazonaws.com"

# 3. build + tag + push
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
REPO="${ACCOUNT_ID}.dkr.ecr.us-west-2.amazonaws.com/comms-openkit-channels"
docker build -f deploy/Dockerfile -t "${REPO}:v0.1.0" .
docker push "${REPO}:v0.1.0"

# put the URI in terraform.tfvars as container_image
```

Apple Silicon: add `--platform linux/amd64` to `docker build` (Fargate
defaults to amd64).

## Deploy

```sh
cd deploy/terraform/aws
cp terraform.tfvars.example terraform.tfvars
# edit terraform.tfvars — real ARNs, real container_image, real cert_arn

terraform init
terraform plan       # READ every proposed resource, especially the RDS block
terraform apply
```

The first apply provisions the Aurora cluster — expect ~10 minutes. Outputs
include `channels_url`, `db_endpoint`, `db_password_secret_arn`, and
`kms_key_arn`.

Point your webhook at the URL via `CHANNELS_URL`. Use the same
`INTERNAL_API_TOKEN` secret on both services.

## About the DB password

By default `db_master_password = ""` and the module generates a random 32-char
password, stores it in Secrets Manager as `${service_name}-db-password`, and
injects it into the container as `DB_PASSWORD` via the task definition's
`secrets` block. For production, source your own Secrets Manager secret
outside this module and pass its value in `db_master_password`, then leave the
`aws_secretsmanager_secret_version` alone (or refactor to skip it).

The app's `DATABASE_URL` needs the assembled DSN. The task definition emits
`DB_HOST`, `DB_PORT`, `DB_NAME`, `DB_USERNAME` as plain env vars and
`DB_PASSWORD` as a secret; assemble `DATABASE_URL` in your entrypoint / a
small init script, e.g.:

```sh
export DATABASE_URL="postgres://${DB_USERNAME}:${DB_PASSWORD}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=require"
exec /server
```

Or override the image's ENTRYPOINT with a two-line shell wrapper. This keeps
the DB password out of Terraform state's plaintext env-var block and out of
CloudWatch logs.

## PostGIS extension

Aurora Postgres does NOT install PostGIS by default. After the cluster is up,
connect once as the master user and run:

```sql
CREATE EXTENSION IF NOT EXISTS postgis;
```

Do this before the first app boot — the schema's `messages.body_location`
depends on it. This is a one-time privileged operation the app's migration
role intentionally cannot perform.

## Point a real hostname at it

Create an ALIAS/CNAME in your DNS provider from
`channels.example.com` -> `<alb_dns_name>` (Route53: A ALIAS record;
elsewhere: CNAME).

## Tear down

```sh
terraform destroy
```

This deletes the ALB, ECS, log group, IAM roles, security groups, Secrets
Manager secret, and — importantly — **the Aurora cluster and all its data**
(because `skip_final_snapshot = true` in this reference module).

The KMS key stays in `PendingDeletion` for `deletion_window_in_days` (30 by
default) before it's actually destroyed; you can `terraform state rm` it and
schedule deletion by hand if you want it gone sooner.

It does NOT delete:

- The ACM certificate (managed outside this module)
- The `INTERNAL_API_TOKEN` Secrets Manager secret (managed outside)
- The ECR repo + images (delete manually if you're done)
- Anything you created outside Terraform

## Extending

Common changes for production:

- **Private subnets everywhere.** Set `public_subnet_ids` to your private
  app subnets, `db_subnet_ids` to your isolated DB subnets, flip
  `assign_public_ip = false` on the ECS service, route egress through NAT or
  VPC endpoints (KMS, Secrets Manager, ECR API/DKR, CloudWatch Logs, SES).
- **Deletion protection + final snapshot.** Set `deletion_protection = true`
  and `skip_final_snapshot = false` on `aws_rds_cluster.db`. Keep the CMK
  outside Terraform state or set a longer deletion window.
- **Reader instance.** Add a second `aws_rds_cluster_instance` with the
  same cluster identifier for a hot standby / read replica.
- **Autoscale ECS.** Add `aws_appautoscaling_target` + policies on
  `service_namespace = "ecs"` targeting CPU or request count.
- **Alarms.** Add `aws_cloudwatch_metric_alarm` on ALB 5xx rate, ECS task
  restarts, and Aurora `CPUUtilization` / `FreeableMemory`.
- **Access logs.** Enable ALB + Aurora audit logging into S3 / CloudWatch.
- **PGaudit + slow-query log.** Add a custom `aws_rds_cluster_parameter_group`.

## Notes on the module design

- **Terraform module boundary is the deploy contract.** The app is configured
  entirely through env vars (see `.env.example`). This module's job is to
  provision the DB, the KMS key, and to inject the right env vars — half
  plain, half from Secrets Manager ARNs.
- **KMS is customer-managed.** The app calls
  `kms:GenerateDataKey`/`kms:Decrypt` against the CMK this module creates, so
  every rotation, every audit-log line, every DENY on the key stays under
  your control.
- **Task-execution role only reads the secrets you pass.** No wildcards.
- **No cross-tenant DB user.** Only the master user exists — extend the
  module with per-service IAM auth users (`aws_rds_cluster` supports it) if
  you want to move off password-based auth.
