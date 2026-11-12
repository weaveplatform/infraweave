// Package sqltest gives each test its own PostgreSQL database (decision
// 0016). A test package calls Main from TestMain with the migration streams
// its tests need; the first DSN, Open or Empty then starts one server for the
// whole binary, from test/deps' pinned PostgreSQL image, and migrates
// Template with those streams. INFRAWEAVE_POSTGRES=next runs every suite on
// the next major instead, as the postgres-next job does. Each DSN or Open creates a database copied from
// Template, so it starts migrated, and Empty one with nothing applied, for a
// test that applies streams itself; each is dropped when its test ends.
// Server starts a separate server for a test that needs another major
// version or stops the server.
//
// Without a Docker daemon a test skips, unless RequireDocker is set to 1, as
// CI sets it: there a missing daemon fails the test, so a database test never
// passes by skipping.
//
// # Input and output
//
// It starts and stops containers through testcontainers, which finds the
// daemon through DOCKER_HOST, the Docker context or the default socket. It
// reads RequireDocker and INFRAWEAVE_POSTGRES from the environment and
// reports through testing.TB.
//
// # Dependencies
//
// It imports testcontainers and its postgres module, pgx, platform/sql for
// Open, platform/sql/migrate for Template and test/deps for the image
// pins. Only tests import it.
package sqltest
