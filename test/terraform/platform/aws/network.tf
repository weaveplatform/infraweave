# The VPC: a public subnet in each of two availability zones for the load
# balancer and the NAT gateway, and a private one in each for the tasks and
# RDS, which has no public address. One NAT gateway serves both private
# subnets: the lab trades zone-independent egress for half the hourly NAT
# cost.

resource "aws_vpc" "platform" {
  cidr_block           = var.vpc_cidr
  enable_dns_support   = true
  enable_dns_hostnames = true

  tags = {
    Name = local.name
  }
}

resource "aws_internet_gateway" "platform" {
  vpc_id = aws_vpc.platform.id

  tags = {
    Name = local.name
  }
}

# /24s of the VPC: indexes 0 and 1 public, 8 and 9 private, which any VPC
# of /20 or larger holds. A /24 leaves the load balancer the free addresses
# it needs to scale.
resource "aws_subnet" "public" {
  count             = length(local.zones)
  vpc_id            = aws_vpc.platform.id
  availability_zone = local.zones[count.index]
  cidr_block        = cidrsubnet(var.vpc_cidr, 24 - tonumber(split("/", var.vpc_cidr)[1]), count.index)

  tags = {
    Name = "${local.name}-public-${local.zones[count.index]}"
  }
}

resource "aws_subnet" "private" {
  count             = length(local.zones)
  vpc_id            = aws_vpc.platform.id
  availability_zone = local.zones[count.index]
  cidr_block        = cidrsubnet(var.vpc_cidr, 24 - tonumber(split("/", var.vpc_cidr)[1]), 8 + count.index)

  tags = {
    Name = "${local.name}-private-${local.zones[count.index]}"
  }
}

resource "aws_eip" "nat" {
  domain = "vpc"

  tags = {
    Name = local.name
  }

  depends_on = [aws_internet_gateway.platform]
}

resource "aws_nat_gateway" "platform" {
  allocation_id = aws_eip.nat.id
  subnet_id     = aws_subnet.public[0].id

  tags = {
    Name = local.name
  }

  depends_on = [aws_internet_gateway.platform]
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.platform.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.platform.id
  }

  tags = {
    Name = "${local.name}-public"
  }
}

resource "aws_route_table_association" "public" {
  count          = length(aws_subnet.public)
  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}

# The tasks reach GHCR, KMS, CloudWatch Logs and the VCS providers' APIs
# through the NAT gateway.
resource "aws_route_table" "private" {
  vpc_id = aws_vpc.platform.id

  route {
    cidr_block     = "0.0.0.0/0"
    nat_gateway_id = aws_nat_gateway.platform.id
  }

  tags = {
    Name = "${local.name}-private"
  }
}

resource "aws_route_table_association" "private" {
  count          = length(aws_subnet.private)
  subnet_id      = aws_subnet.private[count.index].id
  route_table_id = aws_route_table.private.id
}

# The tasks' object traffic reaches S3 without crossing the NAT gateway,
# which would bill it per gigabyte. Worker Pods fetch signed URLs from S3's
# public endpoint and do not use it.
resource "aws_vpc_endpoint" "s3" {
  vpc_id            = aws_vpc.platform.id
  service_name      = "com.amazonaws.${var.region}.s3"
  vpc_endpoint_type = "Gateway"
  route_table_ids   = [aws_route_table.private.id]

  tags = {
    Name = "${local.name}-s3"
  }
}

resource "aws_security_group" "alb" {
  name        = "${local.name}-alb"
  description = "infraweave lab load balancer: HTTPS in, the tasks' listeners out"
  vpc_id      = aws_vpc.platform.id
}

resource "aws_security_group" "tasks" {
  name        = "${local.name}-tasks"
  description = "infraweave lab server tasks: the load balancer in, HTTPS and PostgreSQL out"
  vpc_id      = aws_vpc.platform.id
}

resource "aws_security_group" "database" {
  name        = "${local.name}-database"
  description = "infraweave lab RDS for PostgreSQL: the tasks in"
  vpc_id      = aws_vpc.platform.id
}

resource "aws_vpc_security_group_ingress_rule" "alb_https" {
  for_each          = toset(var.ingress_cidrs)
  security_group_id = aws_security_group.alb.id
  description       = "HTTPS"
  ip_protocol       = "tcp"
  from_port         = 443
  to_port           = 443
  cidr_ipv4         = each.value
}

# server.listen and server.admin_listen: the API, and the probes the
# target group's health check reads.
locals {
  ports = {
    api   = 8080
    admin = 9090
  }
}

resource "aws_vpc_security_group_egress_rule" "alb_tasks" {
  for_each                     = local.ports
  security_group_id            = aws_security_group.alb.id
  description                  = "server ${each.key} listener"
  ip_protocol                  = "tcp"
  from_port                    = each.value
  to_port                      = each.value
  referenced_security_group_id = aws_security_group.tasks.id
}

resource "aws_vpc_security_group_ingress_rule" "tasks_alb" {
  for_each                     = local.ports
  security_group_id            = aws_security_group.tasks.id
  description                  = "server ${each.key} listener"
  ip_protocol                  = "tcp"
  from_port                    = each.value
  to_port                      = each.value
  referenced_security_group_id = aws_security_group.alb.id
}

# HTTPS to GHCR, S3, KMS, STS, CloudWatch Logs and the VCS providers.
resource "aws_vpc_security_group_egress_rule" "tasks_https" {
  security_group_id = aws_security_group.tasks.id
  description       = "HTTPS"
  ip_protocol       = "tcp"
  from_port         = 443
  to_port           = 443
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_vpc_security_group_egress_rule" "tasks_database" {
  security_group_id            = aws_security_group.tasks.id
  description                  = "PostgreSQL"
  ip_protocol                  = "tcp"
  from_port                    = 5432
  to_port                      = 5432
  referenced_security_group_id = aws_security_group.database.id
}

resource "aws_vpc_security_group_ingress_rule" "database_tasks" {
  security_group_id            = aws_security_group.database.id
  description                  = "PostgreSQL from the server tasks"
  ip_protocol                  = "tcp"
  from_port                    = 5432
  to_port                      = 5432
  referenced_security_group_id = aws_security_group.tasks.id
}
