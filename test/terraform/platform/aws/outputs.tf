# What the acceptance runner and the worker roots attach with. Every
# endpoint is below server.public_url, whose certificate ACM issues from a
# public CA, so ca_certificate is null: clients trust it with their system
# roots, as they do the S3 endpoint signed URLs point at.
output "platform" {
  value = {
    schema     = 1
    kind       = "platform"
    provider   = "aws"
    owner      = var.ownership.owner
    batch      = var.ownership.batch
    expires_at = var.ownership.expires_at
    cloud = {
      account_id = local.account_id
      region     = var.region
    }
    public_url     = local.public_url
    ca_certificate = null
    endpoints = {
      api      = "${local.public_url}/api/v1"
      operator = "${local.public_url}/operator/v1"
      worker   = "${local.public_url}/worker/v1"
      state    = "${local.public_url}/state/v1"
    }
    # The address the tasks reach GHCR and the VCS providers from, for an
    # allow list on their side.
    egress_ips = [aws_eip.nat.public_ip]
    bucket = {
      name   = aws_s3_bucket.objects.bucket
      region = var.region
    }
    keys = {
      kek        = aws_kms_key.kek.arn
      signing    = aws_kms_key.signing.arn
      federation = aws_kms_key.federation.arn
    }
    log_group = aws_cloudwatch_log_group.server.name
  }

  # The service is steady only once both tasks pass the health check.
  depends_on = [aws_ecs_service.platform]
}

# Every resource this root creates, and the zone it borrows, for cleanup's
# readback: an owned entry is absent once the owner tag search no longer
# returns an ARN ending in its id. Route 53 records carry no tags, so the
# search never returns them; the empty state is their evidence.
locals {
  regional = concat(
    [
      { kind = "ec2:vpc", id = aws_vpc.platform.arn },
      { kind = "ec2:internet-gateway", id = aws_internet_gateway.platform.arn },
      { kind = "ec2:elastic-ip", id = aws_eip.nat.arn },
      { kind = "ec2:natgateway", id = aws_nat_gateway.platform.id },
      { kind = "ec2:route-table", id = aws_route_table.public.arn },
      { kind = "ec2:route-table", id = aws_route_table.private.arn },
      { kind = "ec2:vpc-endpoint", id = aws_vpc_endpoint.s3.arn },
      { kind = "ec2:security-group", id = aws_security_group.alb.arn },
      { kind = "ec2:security-group", id = aws_security_group.tasks.arn },
      { kind = "ec2:security-group", id = aws_security_group.database.arn },
      { kind = "acm:certificate", id = aws_acm_certificate.platform.arn },
      { kind = "elasticloadbalancing:loadbalancer", id = aws_lb.platform.arn },
      { kind = "elasticloadbalancing:targetgroup", id = aws_lb_target_group.api.arn },
      { kind = "elasticloadbalancing:listener", id = aws_lb_listener.https.arn },
      { kind = "rds:db", id = aws_db_instance.platform.arn },
      { kind = "rds:subgrp", id = aws_db_subnet_group.platform.arn },
      { kind = "rds:pg", id = aws_db_parameter_group.platform.arn },
      { kind = "secretsmanager:secret", id = aws_db_instance.platform.master_user_secret[0].secret_arn },
      { kind = "s3:bucket", id = aws_s3_bucket.objects.arn },
      { kind = "kms:key", id = aws_kms_key.kek.arn },
      { kind = "kms:key", id = aws_kms_key.signing.arn },
      { kind = "kms:key", id = aws_kms_key.federation.arn },
      { kind = "logs:log-group", id = aws_cloudwatch_log_group.server.arn },
      { kind = "ecs:cluster", id = aws_ecs_cluster.platform.arn },
      { kind = "ecs:service", id = aws_ecs_service.platform.id },
      { kind = "ecs:task-definition", id = aws_ecs_task_definition.platform.arn },
    ],
    [for s in concat(aws_subnet.public, aws_subnet.private) : { kind = "ec2:subnet", id = s.arn }],
  )
  global = [
    { kind = "iam:role", id = aws_iam_role.execution.arn },
    { kind = "iam:role", id = aws_iam_role.task.arn },
    { kind = "route53:record", id = "${var.route53_zone_id}/${aws_route53_record.platform.fqdn}/A" },
    { kind = "route53:record", id = "${var.route53_zone_id}/${aws_route53_record.validation.fqdn}/${aws_route53_record.validation.type}" },
  ]
}

output "inventory" {
  value = concat(
    [for e in local.regional : merge(e, {
      scope     = var.region
      owned     = true
      owner     = var.ownership.owner
      lifecycle = "deployment"
    })],
    [for e in local.global : merge(e, {
      scope     = "global"
      owned     = true
      owner     = var.ownership.owner
      lifecycle = "deployment"
    })],
    [{
      kind      = "route53:hostedzone"
      id        = data.aws_route53_zone.lab.arn
      scope     = "global"
      owned     = false
      owner     = var.ownership.owner
      lifecycle = "external"
    }],
  )
}
