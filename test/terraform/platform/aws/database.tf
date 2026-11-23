# RDS for PostgreSQL in the private subnets. The server connects as
# local.db_role with database.iam_auth aws: every new connection's password
# is an IAM token for the task role, which only TLS may carry, so the
# instance refuses plaintext. The master user's password lives in a Secrets
# Manager secret RDS manages; only the tasks' bootstrap container reads it,
# to create local.db_role.

locals {
  db_name = "infraweave"
  db_role = "infraweave"
}

resource "aws_db_subnet_group" "platform" {
  name       = local.name
  subnet_ids = aws_subnet.private[*].id
}

resource "aws_db_parameter_group" "platform" {
  name   = local.name
  family = "postgres${split(".", var.postgres_version)[0]}"

  parameter {
    name  = "rds.force_ssl"
    value = "1"
  }
}

resource "aws_db_instance" "platform" {
  identifier     = local.name
  engine         = "postgres"
  engine_version = var.postgres_version
  instance_class = var.db_instance_class

  allocated_storage = 20
  storage_type      = "gp3"
  storage_encrypted = true

  db_name                             = local.db_name
  username                            = "infraweave_admin"
  manage_master_user_password         = true
  iam_database_authentication_enabled = true

  db_subnet_group_name   = aws_db_subnet_group.platform.name
  parameter_group_name   = aws_db_parameter_group.platform.name
  vpc_security_group_ids = [aws_security_group.database.id]
  publicly_accessible    = false
  multi_az               = false

  # A lab database lives for one batch: no backups, which also shortens
  # its creation, and nothing kept when it is destroyed.
  backup_retention_period  = 0
  skip_final_snapshot      = true
  delete_automated_backups = true
  deletion_protection      = false
  apply_immediately        = true
}
