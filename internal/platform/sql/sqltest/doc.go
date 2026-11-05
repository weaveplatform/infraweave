// Package sqltest gives each test its own PostgreSQL database (decision
// 0016). A test package calls Main from TestMain with test/acceptance's
// pinned image; the first DSN or Open then starts one server for the whole
// binary, and each call creates a database copied from Template and drops it
// when the test ends. Server starts a separate server for a test that needs
// another major version or stops the server.
//
// Without a Docker daemon a test skips, unless RequireDocker is set to 1, as
// CI sets it: there a missing daemon fails the test, so a database test never
// passes by skipping.
//
// # Input and output
//
// It starts and stops containers through testcontainers, which finds the
// daemon through DOCKER_HOST, the Docker context or the default socket. It
// reads RequireDocker from the environment and reports through testing.TB.
//
// # Dependencies
//
// It imports testcontainers and its postgres module, pgx, and platform/sql
// for Open. Only tests import it.
package sqltest
