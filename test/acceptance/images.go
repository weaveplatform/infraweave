package acceptance

// Every container image the suite starts, pinned by the digest of its
// multi-platform index so a run is repeatable on any host. deps-refresh.yml
// moves each pin to its image's newest release; the tag names the release
// for a reader, and the digest is what Docker pulls.
const (
	// ImagePostgres serves @postgres: PostgreSQL 18 (decision 0005).
	ImagePostgres = "postgres:18.6@sha256:3a4264520e121176619f54ff867d12774bc81e95e7b84db58d63bccdb6144b81"
	// ImageObjectStore serves @objectstore: RustFS, the S3-compatible store
	// decision 0010 names.
	ImageObjectStore = "rustfs/rustfs:1.0.4@sha256:ed9210d6411d77ffceb70d94b964a3cb7d225983368bc25bebe01e995293dcc2"
)
