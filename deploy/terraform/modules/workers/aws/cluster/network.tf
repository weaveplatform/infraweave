# The create branch's network: a public and a private subnet in each
# availability zone, one NAT gateway the private subnets leave through, and
# no inbound path to the nodes. Auto Mode places nodes in the private
# subnets; the public ones hold only the NAT gateway.

locals {
  create_network = var.network.mode == "create"
  zones          = local.create_network ? var.network.create.availability_zones : []
}

resource "aws_vpc" "workers" {
  count                = local.create_network ? 1 : 0
  cidr_block           = var.network.create.vpc_cidr
  enable_dns_support   = true
  enable_dns_hostnames = true
  tags                 = { Name = var.cluster.name }
}

resource "aws_internet_gateway" "workers" {
  count  = local.create_network ? 1 : 0
  vpc_id = aws_vpc.workers[0].id
  tags   = { Name = var.cluster.name }
}

# The public /24s sit at the bottom of the range and the private subnets are
# its third and fourth quarters, so a /20 VPC fits both zones of each.
resource "aws_subnet" "public" {
  count                   = length(local.zones)
  vpc_id                  = aws_vpc.workers[0].id
  availability_zone       = local.zones[count.index]
  cidr_block              = cidrsubnet(var.network.create.vpc_cidr, 24 - tonumber(split("/", var.network.create.vpc_cidr)[1]), count.index)
  map_public_ip_on_launch = false
  tags                    = { Name = "${var.cluster.name}-public-${local.zones[count.index]}" }
}

resource "aws_subnet" "private" {
  count             = length(local.zones)
  vpc_id            = aws_vpc.workers[0].id
  availability_zone = local.zones[count.index]
  cidr_block        = cidrsubnet(var.network.create.vpc_cidr, 2, count.index + 2)
  tags = {
    Name = "${var.cluster.name}-private-${local.zones[count.index]}"
    # Auto Mode's internal load balancers would select these; it starts none.
    "kubernetes.io/role/internal-elb" = "1"
  }
}

resource "aws_eip" "nat" {
  count  = local.create_network ? 1 : 0
  domain = "vpc"
  tags   = { Name = var.cluster.name }
}

resource "aws_nat_gateway" "workers" {
  count         = local.create_network ? 1 : 0
  allocation_id = aws_eip.nat[0].id
  subnet_id     = aws_subnet.public[0].id
  tags          = { Name = var.cluster.name }

  depends_on = [aws_internet_gateway.workers]
}

resource "aws_route_table" "public" {
  count  = local.create_network ? 1 : 0
  vpc_id = aws_vpc.workers[0].id
  tags   = { Name = "${var.cluster.name}-public" }
}

resource "aws_route" "public_internet" {
  count                  = local.create_network ? 1 : 0
  route_table_id         = aws_route_table.public[0].id
  destination_cidr_block = "0.0.0.0/0"
  gateway_id             = aws_internet_gateway.workers[0].id
}

resource "aws_route_table" "private" {
  count  = local.create_network ? 1 : 0
  vpc_id = aws_vpc.workers[0].id
  tags   = { Name = "${var.cluster.name}-private" }
}

resource "aws_route" "private_nat" {
  count                  = local.create_network ? 1 : 0
  route_table_id         = aws_route_table.private[0].id
  destination_cidr_block = "0.0.0.0/0"
  nat_gateway_id         = aws_nat_gateway.workers[0].id
}

resource "aws_route_table_association" "public" {
  count          = length(local.zones)
  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public[0].id
}

resource "aws_route_table_association" "private" {
  count          = length(local.zones)
  subnet_id      = aws_subnet.private[count.index].id
  route_table_id = aws_route_table.private[0].id
}

locals {
  subnet_ids         = local.create_network ? aws_subnet.private[*].id : var.network.existing.subnet_ids
  security_group_ids = local.create_network ? [] : var.network.existing.security_group_ids
  vpc_id             = local.create_network ? aws_vpc.workers[0].id : var.network.existing.vpc_id
}
