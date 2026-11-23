# Public HTTPS: an Application Load Balancer with an ACM certificate for
# domain_name, validated through DNS in the borrowed Route 53 zone, that
# forwards to the tasks' API listener and checks their health on the admin
# listener. ACM issues from a public CA, so every client, the worker Pods
# among them, trusts it with its system roots.

data "aws_route53_zone" "lab" {
  zone_id = var.route53_zone_id
}

resource "aws_acm_certificate" "platform" {
  domain_name       = var.domain_name
  validation_method = "DNS"

  lifecycle {
    create_before_destroy = true

    precondition {
      condition = (
        var.domain_name == trimsuffix(data.aws_route53_zone.lab.name, ".") ||
        endswith(var.domain_name, ".${trimsuffix(data.aws_route53_zone.lab.name, ".")}")
      )
      error_message = "domain_name must lie in the Route 53 zone route53_zone_id names."
    }
  }
}

# The certificate names domain_name alone, so ACM asks for one record.
locals {
  validation = one(aws_acm_certificate.platform.domain_validation_options)
}

resource "aws_route53_record" "validation" {
  zone_id         = var.route53_zone_id
  name            = local.validation.resource_record_name
  type            = local.validation.resource_record_type
  records         = [local.validation.resource_record_value]
  ttl             = 60
  allow_overwrite = true
}

resource "aws_acm_certificate_validation" "platform" {
  certificate_arn         = aws_acm_certificate.platform.arn
  validation_record_fqdns = [aws_route53_record.validation.fqdn]
}

resource "aws_lb" "platform" {
  name                       = local.name
  load_balancer_type         = "application"
  internal                   = false
  subnets                    = aws_subnet.public[*].id
  security_groups            = [aws_security_group.alb.id]
  drop_invalid_header_fields = true
  # A worker's poll is held for up to 120 seconds with no byte sent, so
  # the balancer's idle timeout exceeds it.
  # ref: internal/workers/internal/core/worker/worker.go MaxWait
  idle_timeout = 150
}

resource "aws_lb_target_group" "api" {
  name        = local.name
  vpc_id      = aws_vpc.platform.id
  target_type = "ip"
  protocol    = "HTTP"
  port        = local.ports.api
  # Longer than server.shutdown.ready_delay, so a stopping task is out of
  # rotation before it refuses connections.
  deregistration_delay = 30

  health_check {
    protocol            = "HTTP"
    port                = tostring(local.ports.admin)
    path                = "/readyz"
    matcher             = "200"
    interval            = 10
    timeout             = 5
    healthy_threshold   = 2
    unhealthy_threshold = 3
  }
}

resource "aws_lb_listener" "https" {
  load_balancer_arn = aws_lb.platform.arn
  port              = 443
  protocol          = "HTTPS"
  ssl_policy        = "ELBSecurityPolicy-TLS13-1-2-2021-06"
  certificate_arn   = aws_acm_certificate_validation.platform.certificate_arn

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.api.arn
  }
}

resource "aws_route53_record" "platform" {
  zone_id = var.route53_zone_id
  name    = var.domain_name
  type    = "A"

  alias {
    name                   = aws_lb.platform.dns_name
    zone_id                = aws_lb.platform.zone_id
    evaluate_target_health = false
  }
}
