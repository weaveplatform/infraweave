// Package worker is the workers' logic. Service registers a worker of a
// private pool from a compact ES256 JWS whose x5c certificate chains to
// the pool CA and carries the pool's current serial, refusing a skewed
// iat, a replayed jti, an unsupported launcher version and a new worker
// beyond the organization's private-workers limit, or resumes the worker
// the registration names; claims an idle, undrained worker's next run of
// its pool through the workersapi.Scheduler BindScheduler binds, under
// the worker's row lock and with a run token, then holds its poll, with
// no transaction open, until a wake-up brings a run, the worker is
// cycled, its drain flag changes or its wait ends, renewing a session
// token near its expiry; and deregisters it. Wake and WakeAll end the
// holds of this replica's polls, which reread their workers and claim
// again, WakeOne ends the hold of the newest idle worker's poll of a
// pool, and Stop answers them at once as the replica shuts down. For
// the API it drains and undrains a worker, cycles a pool's workers, and
// searches, suggests and lists them. A worker's Status is DRAINED, BUSY
// or IDLE.
//
// ref: decision 0008 § "Claiming runs", § "The worker API", § "Worker
// status, drain, cycle and reset"
//
// # Input and output
//
// New takes the Store, the pool registrations and claims begin
// transactions on, the worker session and run tokens it mints, the limits
// it checks, the server's version and the longest hold; BindScheduler
// takes the runs domain's claim.
//
// # Dependencies
//
// It imports go-jose, the access, features and workers api packages,
// internal/kernel and its search package, platform/tx and the domain's
// pool package. The domain's assembly constructs it over the pg adapter.
package worker
