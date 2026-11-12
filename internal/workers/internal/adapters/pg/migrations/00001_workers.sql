-- Workers: private worker pools, their workers and the pool CA.
-- ref: decisions 0004, 0008 and 0013

-- The installation's one pool CA, an ECDSA P-256 key, created on first
-- use. id is always 1, so replicas that create it together keep one.
-- certificate is its certificate's DER, and private_key_sealed its key's
-- PKCS #8 DER, sealed to the row. next_serial is the serial the next
-- certificate it signs takes, so no two pools' certificates share one.
CREATE TABLE worker_pool_ca (
    id                 smallint    PRIMARY KEY CHECK (id = 1),
    certificate        bytea       NOT NULL,
    private_key_sealed bytea       NOT NULL,
    next_serial        bigint      NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE worker_pool_ca IS
    'global: the installation''s one pool CA, which signs every account''s pool certificates';

-- A private worker pool. name is unique per account and may change.
-- organization_id is its account's organization, which a registration
-- counts the organization's workers in, since a launcher names only the
-- pool.
-- space_id has no foreign key because spaces belong to the access domain:
-- a pool holds its space while it is written, and deleting a space asks
-- whether a pool lives in it, which worker_pools_space answers.
-- certificate is the DER of the certificate the pool CA last signed for
-- the pool's key, and serial its serial: a registration under an older
-- one is refused. preparing_timeout_seconds NULL takes the installation's
-- default; drift_detection_run_limit NULL or negative is no limit.
CREATE TABLE worker_pools (
    account_id                uuid        NOT NULL,
    organization_id           uuid        NOT NULL,
    id                        uuid        NOT NULL,
    name                      text        NOT NULL,
    space_id                  uuid        NOT NULL,
    description               text        NOT NULL DEFAULT '',
    labels                    text[]      NOT NULL DEFAULT '{}',
    preparing_timeout_seconds integer     CHECK (preparing_timeout_seconds > 0),
    drift_detection_run_limit integer,
    certificate               bytea       NOT NULL,
    serial                    bigint      NOT NULL,
    created_at                timestamptz NOT NULL DEFAULT now(),
    updated_at                timestamptz NOT NULL DEFAULT now(),
    version                   bigint      NOT NULL DEFAULT 1,
    PRIMARY KEY (account_id, id),
    UNIQUE (account_id, name)
);

CREATE INDEX worker_pools_space ON worker_pools (account_id, space_id);

-- A registration names the pool alone.
CREATE UNIQUE INDEX worker_pools_id ON worker_pools (id);

-- A registered worker of a pool, until it deregisters or its pool is
-- deleted. serial is the pool certificate's serial it registered under,
-- which a reset leaves behind. last_seen_at is its last call, which marks
-- it offline when old; available_at is when it last became idle.
-- cycle is set by workerPoolCycle and answered on each poll until the
-- worker deregisters. organization_id is its pool's, which the
-- private-workers limit counts by.
CREATE TABLE workers (
    account_id      uuid        NOT NULL,
    organization_id uuid        NOT NULL,
    id              uuid        NOT NULL,
    pool_id         uuid        NOT NULL,
    serial          bigint      NOT NULL,
    version         text        NOT NULL DEFAULT '',
    os              text        NOT NULL DEFAULT '',
    arch            text        NOT NULL DEFAULT '',
    metadata        jsonb       NOT NULL DEFAULT '{}',
    busy            boolean     NOT NULL DEFAULT false,
    drained         boolean     NOT NULL DEFAULT false,
    cycle           boolean     NOT NULL DEFAULT false,
    last_seen_at    timestamptz NOT NULL DEFAULT now(),
    available_at    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, id)
);

CREATE INDEX workers_pool ON workers (account_id, pool_id);

-- A worker session token names the worker alone.
CREATE UNIQUE INDEX workers_id ON workers (id);

CREATE INDEX workers_organization ON workers (organization_id);

-- A registration's jti, kept until expires_at, so a replayed
-- registration is refused.
CREATE TABLE worker_nonces (
    account_id uuid        NOT NULL,
    pool_id    uuid        NOT NULL,
    jti        text        NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (account_id, pool_id, jti)
);

-- An account's default runner images, which accountUpdateDefaultWorkerPoolRunnerImages
-- sets: the image a run on a private or a public pool uses when neither
-- its runtime configuration nor its stack names one. NULL takes the
-- launcher's default. An account with neither set has no row.
CREATE TABLE worker_runner_images (
    account_id    uuid        PRIMARY KEY,
    private_image text,
    public_image  text,
    updated_at    timestamptz NOT NULL DEFAULT now()
);
