-- Deployment metadata contains IDs and source coordinates, never credentials.
-- The first eligible event fixes a run's binding and environment. Native
-- GitLab deployments can be shared by runs with the same environment/ref/SHA.
CREATE TABLE vcs_deployment_runs (
    account_id uuid NOT NULL,
    run_id uuid NOT NULL,
    binding jsonb NOT NULL,
    PRIMARY KEY (account_id, run_id)
);

-- Queue uniqueness covers live jobs only. This receipt also prevents a
-- repeated event from recreating a completed report for the same run/state.
CREATE TABLE vcs_deployment_states (
    account_id uuid NOT NULL,
    run_id uuid NOT NULL,
    state text NOT NULL,
    PRIMARY KEY (account_id, run_id, state)
);

-- The epoch is the first eligible run transition, not run creation order.
-- Reserving the newest epoch at enqueue prevents a late older run from
-- regressing a shared native deployment, including while the newer job retries.
CREATE TABLE vcs_deployment_resources (
    account_id uuid NOT NULL,
    resource text NOT NULL,
    epoch_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    run_id uuid NOT NULL,
    sequence bigint NOT NULL DEFAULT 0 CHECK (sequence >= 0),
    PRIMARY KEY (account_id, resource)
);
