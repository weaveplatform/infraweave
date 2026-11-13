// Package deps starts the Docker dependencies of the TestAcc tests: the
// acceptance suite's and those beside the code. Dependencies starts each
// one the first time a test asks for it and shares it with every later
// test of the test binary: PostgreSQL, a fresh database per caller copied
// from a migrated template, a caller's own PostgreSQL on a fixed port
// that stops, starts and pauses, and one below the supported floor;
// RustFS; Vault in dev mode with the Transit engine; Gitea, serving HTTPS
// under a CA made for the run; and the in-process OIDC issuer. images.go
// pins each image by digest, and Postgres picks the PostgreSQL major the
// INFRAWEAVE_POSTGRES variable names. tools.go pins the Terraform and
// OpenTofu releases @terraform and @tofu run, and Tool downloads one's
// Linux archive into a cache that outlives the test binary, checks it
// against its pinned SHA-256 on each use, and extracts its binary once
// per test binary. Eventually is the only way a TestAcc
// test waits, polling with backoff up to WaitLimit. DockerReachable
// reports whether a daemon answers, and KEK is the test key every server
// runs with.
//
// ref: decision 0016
//
// # Input and output
//
// It starts and removes containers through the Docker daemon
// testcontainers finds, writes the Gitea CA's files to a temporary
// directory, calls Vault's and Gitea's APIs, and downloads the tools'
// archives over HTTPS into the user's cache directory. It logs nothing.
//
// # Dependencies
//
// testcontainers-go and the moby client run the containers, and the
// OIDC issuer serves on httptest. It imports platform/sql for the pool
// size each server opens, which sizes PostgreSQL's max_connections, and
// nothing of the acceptance suite, which imports it.
package deps
