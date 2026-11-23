# The dedicated AWS account of the cloud tests: a member account of the
# Organization this root creates, alone in an organizational unit of its
# own, so a service control policy for the lab reaches nothing else.
# Organizations creates the account with an administrator role that the
# management account assumes; the other platform roots apply into the
# account through it.

data "aws_partition" "current" {}

# Creating the Organization makes the account of this root's credentials
# its management account, which pays for every member account. Creating
# an OU and an account needs no trusted service, so none is enabled. An
# account that already manages an Organization imports it instead
# (terraform import aws_organizations_organization.lab <organization ID>).
# Organizations refuses to delete an Organization that still holds a
# member account, including one closed less than 90 days ago, so a destroy
# leaves it in place until the lab account is gone.
resource "aws_organizations_organization" "lab" {
  feature_set                   = "ALL"
  aws_service_access_principals = []
  enabled_policy_types          = []
}

resource "aws_organizations_organizational_unit" "lab" {
  name      = var.account_name
  parent_id = coalesce(var.parent_id, aws_organizations_organization.lab.roots[0].id)
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
