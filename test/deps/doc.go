// Package deps starts or borrows the real dependencies of the TestAcc tests: the
// acceptance suite's and those beside the code. Dependencies starts each
// one the first time a test asks for it and shares it with every later
// test of the test binary: PostgreSQL, a fresh database per caller copied
// from a migrated template, a caller's own PostgreSQL on a fixed port
// that stops, starts and pauses, and one below the supported floor;
// RustFS; Vault in dev mode with the Transit engine; Gitea, serving HTTPS
// under a CA made for the run; and navikt's mock-oauth2-server over TLS,
// whose every path segment is an OpenID Provider of its own. Issuer is
// the in-process OpenID Provider, which a scenario starts as its own: it
// runs the authorization code flow with PKCE for the identities a scenario
// queues, with the faults each chooses, serves RP-initiated logout and
// sends back-channel logout tokens. SignIn plays the browser at either
// provider, filling in navikt's sign-in form, and CodeFlow plays the
// relying party, exchanging the code and verifying the ID token. images.go
// pins each image by digest, and Postgres picks the PostgreSQL major the
// INFRAWEAVE_POSTGRES variable names. tools.go pins the Terraform,
// OpenTofu and Terragrunt releases @terraform, @tofu and @terragrunt run,
// and Tool downloads one's Linux build into a cache that outlives the
// test binary, checks it against its pinned SHA-256 on each use, and
// extracts its binary once per test binary, or copies it, Terragrunt's
// build being the binary itself; Release adds the release's SHA256SUMS,
// which must list that SHA-256, and its signature, for a tool mirror to
// serve under the vendor's file names. Eventually
// is the only way a TestAcc
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
// directory, calls Vault's, Gitea's and the OpenID Providers' APIs, trusts
// the certificate navikt's server presents at start, and downloads the tools'
// archives, SHA256SUMS and signatures over HTTPS into the user's cache
// directory. It logs nothing.
//
// UseLab verifies a private Terraform deployment manifest and borrows its
// PostgreSQL, object storage and TLS Gitea without starting replacements.
// Each borrowing process owns its database template and fixture namespace;
// Terraform owns and verifies infrastructure destruction.
// WorkerHosting reads a managed cloud batch's workers manifest, the
// INFRAWEAVE_LAB_WORKERS file, and fails unless it belongs to the borrowed
// deployment; it calls no cloud API.
//
// # Dependencies
//
// testcontainers-go and the moby client run the containers, and the
// in-process OIDC issuer serves on httptest with chi. It imports platform/sql for the pool
// size each server opens, which sizes PostgreSQL's max_connections, and
// nothing of the acceptance suite, which imports it.
package deps
