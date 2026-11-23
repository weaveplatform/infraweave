# The server: an ECS Fargate service of two tasks running infraweave all,
# each holding the same configuration file.
#
# The configuration's auth.signing.kms_keys is a list of mappings, which no
# INFRAWEAVE_* environment variable can carry, so the server reads a file
# named by --config. Fargate mounts no file from outside the task, so each
# task's bootstrap container writes it into a volume the server container
# mounts read-only, and runs to completion before the server starts. The
# same container creates the server's database role: RDS creates only the
# master user, and an IAM-authenticated role needs rds_iam granted in SQL.

locals {
  config_dir  = "/etc/infraweave"
  config_file = "${local.config_dir}/config.yaml"

  # The server's configuration file, decoded strictly: every key here is
  # one infraweave knows, and a key it does not know fails the start.
  config = {
    server = {
      public_url   = local.public_url
      listen       = ":${local.ports.api}"
      admin_listen = ":${local.ports.admin}"
      timeouts = {
        # Longer than the load balancer's idle timeout, so the balancer
        # rather than the server closes an idle connection and never sends
        # a request on one the server has closed.
        idle = "${aws_lb.platform.idle_timeout + 30}s"
      }
    }
    log = {
      level = var.log_level
    }
    database = {
      # No password: database.iam_auth aws sets an IAM token as each new
      # connection's password, signed for AWS_REGION. sslmode require
      # encrypts the connection without verifying RDS's certificate, whose
      # CA is not among the image's system roots.
      url      = "postgres://${local.db_role}@${aws_db_instance.platform.address}:${aws_db_instance.platform.port}/${local.db_name}?sslmode=require"
      iam_auth = "aws"
    }
    objectstore = {
      driver = "s3"
      s3 = {
        bucket = aws_s3_bucket.objects.bucket
        region = var.region
      }
    }
    secrets = {
      kek = {
        provider = "awskms"
        awskms = {
          key_id = aws_kms_key.kek.arn
          region = var.region
        }
      }
    }
    auth = {
      signing = {
        backend  = "kms"
        kms_keys = [{ provider = "awskms", ref = aws_kms_key.signing.arn }]
      }
      operator = {
        keys = var.operator_keys
      }
    }
  }

  # Writes the configuration, then, under a transaction-scoped advisory lock
  # that keeps the two tasks' bootstraps apart, creates the server's role if
  # it is missing, lets it log in with an IAM token (rds_iam), and makes it
  # the database's owner, which owns the public schema the migrations
  # create in. The master user joins the role first, because only a member
  # may hand a database to a role.
  bootstrap_script = <<-EOT
    set -eu
    umask 022
    printf '%s\n' "$CONFIG_YAML" > ${local.config_file}
    psql --no-psqlrc --single-transaction -v ON_ERROR_STOP=1 -v role="$SERVER_ROLE" <<'SQL'
    SELECT pg_advisory_xact_lock(hashtext('infraweave.lab.bootstrap'));
    SELECT format('CREATE ROLE %I LOGIN', :'role') WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'role') \gexec
    GRANT rds_iam TO :"role";
    GRANT :"role" TO CURRENT_USER;
    ALTER DATABASE :"DBNAME" OWNER TO :"role";
    SQL
  EOT
}

resource "aws_cloudwatch_log_group" "server" {
  name              = "/infraweave/lab/${local.name}"
  retention_in_days = var.log_retention_days
}

resource "aws_ecs_cluster" "platform" {
  name = local.name

  setting {
    name  = "containerInsights"
    value = "disabled"
  }
}

# Both roles trust ECS tasks of this account and region alone.
data "aws_iam_policy_document" "ecs_tasks_trust" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "aws:SourceAccount"
      values   = [local.account_id]
    }
    condition {
      test     = "ArnLike"
      variable = "aws:SourceArn"
      values   = ["arn:${local.partition}:ecs:${var.region}:${local.account_id}:*"]
    }
  }
}

# The execution role starts the containers: it writes their logs and hands
# the bootstrap container the master password. The images come from public
# registries, so it pulls with no grant.
resource "aws_iam_role" "execution" {
  name               = "${local.name}-execution"
  assume_role_policy = data.aws_iam_policy_document.ecs_tasks_trust.json
}

data "aws_iam_policy_document" "execution" {
  statement {
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = ["${aws_cloudwatch_log_group.server.arn}:*"]
  }
  statement {
    actions   = ["secretsmanager:GetSecretValue"]
    resources = [aws_db_instance.platform.master_user_secret[0].secret_arn]
  }
}

resource "aws_iam_role_policy" "execution" {
  name   = "execution"
  role   = aws_iam_role.execution.id
  policy = data.aws_iam_policy_document.execution.json
}

# The task role is the server's identity, through the SDKs' default chain.
resource "aws_iam_role" "task" {
  name               = "${local.name}-task"
  assume_role_policy = data.aws_iam_policy_document.ecs_tasks_trust.json
}

data "aws_iam_policy_document" "task" {
  # The s3 driver reads, writes, aborts multipart uploads and deletes, and
  # signs URLs that carry these same grants. s3:ListBucket makes S3 answer
  # 404 rather than 403 for a missing key, which the driver relies on to
  # tell a missing object from a refused one.
  statement {
    sid       = "Objects"
    actions   = ["s3:GetObject", "s3:PutObject", "s3:AbortMultipartUpload", "s3:DeleteObject"]
    resources = ["${aws_s3_bucket.objects.arn}/*"]
  }
  statement {
    sid       = "ObjectKeys"
    actions   = ["s3:ListBucket"]
    resources = [aws_s3_bucket.objects.arn]
  }
  # The awskms KEK provider wraps and unwraps data keys only, each under
  # its encryption context.
  statement {
    sid       = "KEK"
    actions   = ["kms:Encrypt", "kms:Decrypt"]
    resources = [aws_kms_key.kek.arn]
    condition {
      test     = "StringEquals"
      variable = "kms:EncryptionContext:infraweave"
      values   = ["data-key"]
    }
  }
  # The awskms signing backend reads the public key at start and signs
  # digests.
  statement {
    sid       = "Signing"
    actions   = ["kms:GetPublicKey", "kms:Sign"]
    resources = [aws_kms_key.signing.arn]
  }
  statement {
    sid       = "Database"
    actions   = ["rds-db:connect"]
    resources = ["arn:${local.partition}:rds-db:${var.region}:${local.account_id}:dbuser:${aws_db_instance.platform.resource_id}/${local.db_role}"]
  }
}

resource "aws_iam_role_policy" "task" {
  name   = "server"
  role   = aws_iam_role.task.id
  policy = data.aws_iam_policy_document.task.json
}

resource "aws_ecs_task_definition" "platform" {
  family                   = local.name
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = var.task_cpu
  memory                   = var.task_memory
  execution_role_arn       = aws_iam_role.execution.arn
  task_role_arn            = aws_iam_role.task.arn

  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = var.cpu_architecture
  }

  # Task storage, shared by the task's containers and gone with the task.
  volume {
    name = "config"
  }

  container_definitions = jsonencode([
    {
      name       = "bootstrap"
      image      = var.bootstrap_image
      essential  = false
      entryPoint = ["sh", "-c"]
      command    = [local.bootstrap_script]
      environment = [
        { name = "CONFIG_YAML", value = yamlencode(local.config) },
        { name = "SERVER_ROLE", value = local.db_role },
        { name = "PGHOST", value = aws_db_instance.platform.address },
        { name = "PGPORT", value = tostring(aws_db_instance.platform.port) },
        { name = "PGDATABASE", value = local.db_name },
        { name = "PGUSER", value = aws_db_instance.platform.username },
        { name = "PGSSLMODE", value = "require" },
      ]
      secrets = [
        { name = "PGPASSWORD", valueFrom = "${aws_db_instance.platform.master_user_secret[0].secret_arn}:password::" },
      ]
      mountPoints = [
        { sourceVolume = "config", containerPath = local.config_dir, readOnly = false },
      ]
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          awslogs-group         = aws_cloudwatch_log_group.server.name
          awslogs-region        = var.region
          awslogs-stream-prefix = "bootstrap"
        }
      }
    },
    {
      name      = "infraweave"
      image     = var.image
      essential = true
      command   = ["--config", local.config_file]
      environment = [
        { name = "AWS_REGION", value = var.region },
      ]
      portMappings = [for p in values(local.ports) : { containerPort = p, protocol = "tcp" }]
      mountPoints = [
        { sourceVolume = "config", containerPath = local.config_dir, readOnly = true },
      ]
      dependsOn = [
        { containerName = "bootstrap", condition = "SUCCESS" },
      ]
      # server.shutdown's ready_delay and timeout, 5 and 30 seconds, with
      # room to exit before ECS kills the process.
      stopTimeout = 40
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          awslogs-group         = aws_cloudwatch_log_group.server.name
          awslogs-region        = var.region
          awslogs-stream-prefix = "infraweave"
        }
      }
    },
  ])
}

resource "aws_ecs_service" "platform" {
  name             = "infraweave"
  cluster          = aws_ecs_cluster.platform.id
  task_definition  = aws_ecs_task_definition.platform.arn
  desired_count    = 2
  launch_type      = "FARGATE"
  platform_version = "LATEST"

  # The first task applies every migration before its listeners bind.
  health_check_grace_period_seconds = 300

  enable_ecs_managed_tags = true
  propagate_tags          = "SERVICE"

  # An apply returns only once both tasks pass the target group's health
  # check, so the platform output names a platform that answers.
  wait_for_steady_state = true

  deployment_circuit_breaker {
    enable   = true
    rollback = false
  }

  network_configuration {
    subnets          = aws_subnet.private[*].id
    security_groups  = [aws_security_group.tasks.id]
    assign_public_ip = false
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.api.arn
    container_name   = "infraweave"
    container_port   = local.ports.api
  }

  # The tasks need the listener to receive traffic, NAT egress to pull their
  # images, and their roles' grants to start; a destroy removes the service
  # before any of them.
  depends_on = [
    aws_lb_listener.https,
    aws_route_table_association.private,
    aws_iam_role_policy.execution,
    aws_iam_role_policy.task,
  ]
}
