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
// searches, suggests and lists them. A worker's Status is OFFLINE,
// DRAINED, BUSY or IDLE. Reap is worker.reap's pass: it marks workers
// unseen for worker.offline_after offline, deletes each worker unseen for
// run.lost_after in a transaction that fails the runs it held, and fails
// the runs left in preparation past their pool's preparing timeout,
// through the workersapi.Liveness BindLiveness binds. A poll that is not
// busy and names a run fails it there when the worker still holds it, as
// one whose container ended without a report ending it; a poll clears
// its worker's offline mark. Spec answers a run token's bearer its run's handover from the
// scheduler, with the account's default runner image when neither the
// run nor its stack names one, and the run's secrets sealed to the P-256
// key of its pool's current certificate for the run's state version.
//
// ref: decision 0008 § "Claiming runs", § "The worker API", § "Worker
// status, drain, cycle and reset", § "The run specification and its
// secrets", § "Liveness: offline workers and lost runs"
//
// # Input and output
//
// New takes the Store, the pool registrations and claims begin
// transactions on, the worker session and run tokens it mints, the limits
// it checks, the server's version, the longest hold and the reap's
// clocks; BindScheduler takes the runs domain's claim and handover, and
// BindLiveness its liveness.
//
// # Dependencies
//
// It imports go-jose, the access, features and workers api packages,
// internal/kernel and its search package, platform/seal, platform/tx and
// the domain's images and pool packages. The domain's assembly constructs it over the pg adapter.
package worker
