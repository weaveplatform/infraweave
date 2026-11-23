output "account_id" {
  value = aws_organizations_account.lab.id
}

# The role the objectstore, kek and workers-identity roots assume, as their
# landing_zone_role_arn: the administrator role Organizations created in the
# account, which trusts the management account.
output "role_arn" {
  value = "arn:${data.aws_partition.current.partition}:iam::${aws_organizations_account.lab.id}:role/${aws_organizations_account.lab.role_name}"
}

output "organizational_unit_id" {
  value = aws_organizations_organizational_unit.lab.id
}

output "organization_id" {
  value = aws_organizations_organization.lab.id
}
