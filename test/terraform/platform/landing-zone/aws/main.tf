# The dedicated AWS account of the cloud tests: a member account of the
# Organization this root's credentials manage, alone in an organizational
# unit of its own, so a service control policy for the lab reaches nothing
# else. Organizations creates the account with an administrator role that
# the management account assumes; the other platform roots apply into the
# account through it.

data "aws_organizations_organization" "current" {}

data "aws_partition" "current" {}

resource "aws_organizations_organizational_unit" "lab" {
  name      = var.account_name
  parent_id = coalesce(var.parent_id, data.aws_organizations_organization.current.roots[0].id)
}

resource "aws_organizations_account" "lab" {
  name              = var.account_name
  email             = var.email
  parent_id         = aws_organizations_organizational_unit.lab.id
  role_name         = "OrganizationAccountAccessRole"
  close_on_deletion = var.close_on_destroy

  lifecycle {
    # Organizations never returns the role name, so an imported account
    # has none in state, and a role_name change forces a replacement that
    # would close the account.
    ignore_changes = [role_name]
  }
}
