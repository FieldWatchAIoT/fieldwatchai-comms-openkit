# AWS reference deployment — ECS Fargate + ALB + SQS

Minimal, readable Terraform for standing the comms-openkit webhook up on AWS.

> **Reference module, not a production template.** This is intentionally short
> so you can read every resource in one sitting. Review each one and adapt to
> your org's networking, IAM, encryption, and logging policies before
> applying. In particular: the default configuration uses the account's
> **default VPC** with **public subnets** and `assign_public_ip = true` so a
> first-time apply works out of the box. For production, pass your own VPC
> and put the tasks in private subnets behind NAT.

## What this creates

| Resource | Purpose |
|---|---|
| ECS cluster (Fargate) | runs the webhook containers |
| ECS task definition + service | 2 tasks (256 CPU / 512 MB by default) with `awslogs` driver |
| ALB (internet-facing) | HTTPS listener + HTTP -> HTTPS redirect |
| Target group | `/healthz` health check |
| Security groups | ALB accepts 80/443 from anywhere; tasks accept 8080 only from the ALB SG |
| SQS main queue | 60s visibility timeout, 4-day retention |
| SQS dead-letter queue | 14-day retention, redrive after N receives |
| IAM task-execution role | pulls image, reads the referenced Secrets Manager ARNs, writes logs |
| IAM task role | `SendMessage` / `ReceiveMessage` / `DeleteMessage` on the SQS queue only |
| CloudWatch log group | 30-day retention (configurable) |

## Rough cost estimate

At low traffic (a couple of hundred messages/day):

| | Monthly |
|---|---|
| ALB (idle + LCUs) | ~$18 |
| 2 Fargate tasks (0.25 vCPU / 0.5 GB, 24x7) | ~$10 |
| SQS (well under free tier) | ~$0 |
| CloudWatch Logs (30d, low volume) | ~$1 |
| **Total** | **~$25-30 / mo** |

Prices depend on region and traffic. Verify with the AWS pricing calculator
before deploying anything you care about.

## Prerequisites

- Terraform >= 1.6, AWS provider ~> 5.0
- AWS CLI configured (`aws sts get-caller-identity` works)
- An ACM certificate in the same region for whatever hostname you'll front
  the ALB with
- Secrets Manager secrets holding your internal API token + platform secrets
- An ECR repository, and the webhook image built + pushed into it

## Build + push the container image

From the implementation root (`implementations/webhook-go/`):

```sh
# 1. create an ECR repo (once)
aws ecr create-repository --repository-name comms-openkit-webhook --region us-west-2

# 2. auth docker to ECR
aws ecr get-login-password --region us-west-2 \
  | docker login --username AWS --password-stdin \
      "$(aws sts get-caller-identity --query Account --output text).dkr.ecr.us-west-2.amazonaws.com"

# 3. build + tag + push
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
REPO="${ACCOUNT_ID}.dkr.ecr.us-west-2.amazonaws.com/comms-openkit-webhook"
docker build -f deploy/Dockerfile -t "${REPO}:v0.1.0" .
docker push "${REPO}:v0.1.0"

# put the URI in terraform.tfvars as container_image
```

If you're on Apple Silicon, add `--platform linux/amd64` to `docker build`
(Fargate runs amd64 by default). To run arm64 on Fargate instead, set
`runtime_platform` in the task definition to match — not covered here.

## Deploy

```sh
cd deploy/terraform/aws
cp terraform.tfvars.example terraform.tfvars
# edit terraform.tfvars — fill in real ARNs, container_image, channels_url

terraform init
terraform plan       # READ every proposed resource
terraform apply
```

Outputs include `alb_dns_name` and `webhook_url` — configure your platforms
to POST to `${webhook_url}/inbound/<platform-id>?token=<the-secret>` (or the
signed-header equivalent for Twilio/Telegram).

## Point a real hostname at it

Create an ALIAS/CNAME in your DNS provider from
`webhook.example.com` -> `<alb_dns_name>` (Route53: A ALIAS record; anything
else: CNAME).

## Tear down

```sh
terraform destroy
```

This deletes everything the module created (ALB, ECS, SQS, log group, IAM
roles). It does **not** delete:

- The ACM certificate (managed outside this module)
- The Secrets Manager secrets (managed outside this module)
- The ECR repo + images (delete manually if you're done with them)
- Anything you created outside Terraform

## Extending

Common changes:

- **Private subnets + NAT.** Set `public_subnet_ids` to your private
  subnets and flip `assign_public_ip = false` on the ECS service. Route
  egress through a NAT gateway or VPC endpoints (SQS, Secrets Manager,
  ECR API/DKR, CloudWatch Logs).
- **Autoscale.** Add `aws_appautoscaling_target` + policies on
  `service_namespace = "ecs"` targeting CPU or request count.
- **Alarms.** Add `aws_cloudwatch_metric_alarm` on the DLQ's
  `ApproximateNumberOfMessagesVisible` and on ALB 5xx rate.
- **KMS.** Add a customer-managed KMS key to the SQS queue and log group
  for CMK-encrypted-at-rest.
- **Access logs.** Enable ALB access logs to an S3 bucket.

## Notes on the module design

- **Terraform module boundary is the deploy contract.** The app is
  configured entirely through env vars (see `.env.example` in the
  implementation root); the module's job is to inject those env vars
  correctly, half from plain values and half from Secrets Manager ARNs.
- **Task-execution role only reads the secrets you pass.** No wildcards.
  You can safely give this module less-privileged Terraform credentials
  because it never asks for org-wide IAM permissions.
- **The container makes zero Secrets Manager calls itself.** Fargate
  resolves the `secrets` block at task start and injects the resolved
  values as env vars. This keeps the runtime path simple and observable.
