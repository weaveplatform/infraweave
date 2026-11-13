// Package agenttest is the launcher's and the runner's test kit: a fake
// worker API and a fake executor, so their tests run in virtual time with
// no server and no Docker.
//
// NewPool makes a pool's credentials: a P-256 key in PEM and a pool token
// whose certificate certifies it. Server is a fake /worker/v1 of that
// pool, routed by chi and served in process through Client, so a held
// poll waits on a synctest bubble's clock. It checks each registration's
// JWS against the pool's certificate, registers or resumes the worker it
// names, holds polls until Answer or Assign queues a result or the poll's
// wait passes, seals each assigned run's secrets for the pool, answers a
// started report with execute and any other with exit, signs uploads to
// an in-memory store that checks each PUT against its declaration,
// indexes uploaded log chunks once, and records every call. Refuse makes calls fail with a status, Renew hands a worker a new
// session token, and Lose forgets every worker. Executor is a fake executor.Executor whose containers run until
// the test ends them, or fails their wait, and which a restarted launcher
// finds again.
//
// # Input and output
//
// It serves HTTP requests in memory and keeps what it records in memory.
// Nothing is logged.
//
// # Dependencies
//
// It imports chi, go-jose and platform/seal, and internal/agent/executor
// and wire; no server package. Only tests import it.
package agenttest
