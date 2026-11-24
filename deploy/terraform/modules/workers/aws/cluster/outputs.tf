# The cluster stage's contract output, exactly the fields the lab
# coordinator's validate_cluster accepts: the EKS ARN as id, a bare HTTPS
# endpoint and its PEM trust anchor.
output "cluster" {
  value = {
    schema         = 1
    kind           = "worker-cluster"
    owner          = var.ownership.owner
    batch          = var.ownership.batch
    provider       = "aws"
    id             = local.arn
    name           = var.cluster.name
    endpoint       = local.endpoint
    ca_certificate = base64decode(local.ca)
    owned          = local.create
    inventory      = local.inventory
  }
}

output "kubernetes_version" {
  value = local.version
}

output "platform_version" {
  value = local.platform
}

# Every resource this module creates or borrows, for cleanup's readback: an
# owned entry is absent once the owner tag search returns no ARN ending in
# its id. Auto Mode's own instances, launch templates and cluster security
# group are not Terraform's; the coordinator's tag search and the exact
# cluster lookup cover them.
locals {
  owned_entries = local.create ? concat(
    [
      { kind = "eks:cluster", id = local.arn, scope = var.cloud.identifiers.region },
      { kind = "iam:role", id = aws_iam_role.cluster[0].arn, scope = "global" },
      { kind = "iam:role", id = aws_iam_role.node[0].arn, scope = "global" },
    ],
    local.create_network ? concat(
      [
        { kind = "ec2:vpc", id = aws_vpc.workers[0].arn, scope = var.cloud.identifiers.region },
        { kind = "ec2:internet-gateway", id = aws_internet_gateway.workers[0].arn, scope = var.cloud.identifiers.region },
        { kind = "ec2:elastic-ip", id = aws_eip.nat[0].arn, scope = var.cloud.identifiers.region },
        { kind = "ec2:natgateway", id = aws_nat_gateway.workers[0].id, scope = var.cloud.identifiers.region },
        { kind = "ec2:route-table", id = aws_route_table.public[0].arn, scope = var.cloud.identifiers.region },
        { kind = "ec2:route-table", id = aws_route_table.private[0].arn, scope = var.cloud.identifiers.region },
      ],
      [for s in concat(aws_subnet.public, aws_subnet.private) : { kind = "ec2:subnet", id = s.arn, scope = var.cloud.identifiers.region }],
    ) : [],
  ) : []

  borrowed_entries = concat(
    local.create ? [] : [{ kind = "eks:cluster", id = local.arn, scope = var.cloud.identifiers.region }],
    local.create_network ? [] : concat(
      [{ kind = "ec2:vpc", id = var.network.existing.vpc_id, scope = var.cloud.identifiers.region }],
      [for id in var.network.existing.subnet_ids : { kind = "ec2:subnet", id = id, scope = var.cloud.identifiers.region }],
      [for id in var.network.existing.security_group_ids : { kind = "ec2:security-group", id = id, scope = var.cloud.identifiers.region }],
    ),
  )

  inventory = concat(
    [for e in local.owned_entries : merge(e, { owned = true, owner = var.ownership.owner, lifecycle = "cluster" })],
    [for e in local.borrowed_entries : merge(e, { owned = false, owner = var.ownership.owner, lifecycle = "external" })],
  )
}
