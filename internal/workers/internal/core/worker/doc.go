// Package worker is the workers' logic. Service registers a worker of a
// private pool from a compact ES256 JWS whose x5c certificate chains to
// the pool CA and carries the pool's current serial, refusing a skewed
// iat, a replayed jti, an unsupported launcher version and a new worker
// beyond the organization's private-workers limit, or resumes the worker
// the registration names; holds a worker's poll, with no transaction
// open, until it is cycled, its drain flag changes or its wait ends,
// renewing a session token near its expiry; and deregisters it. Wake and
// WakeAll end the holds of this replica's polls, which rereads their
// workers, and Stop answers them at once as the replica shuts down. For
// the API it drains and undrains a worker, cycles a pool's workers, and
// searches, suggests and lists them. A worker's Status is DRAINED, BUSY
// or IDLE.
//
// ref: decision 0008 § "The worker API", § "Worker status, drain, cycle
// and reset"
//
// # Input and output
//
// New takes the Store, the pool registrations begin transactions on,
// the worker session tokens it mints, the limits it checks, the server's
// version and the longest hold.
//
// # Dependencies
//
// It imports go-jose, the access and features api packages,
// internal/kernel and its search package, platform/tx and the domain's
// pool package. The domain's assembly constructs it over the pg adapter.
package worker
