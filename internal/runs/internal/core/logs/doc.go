// Package logs reads a run's logs, which exist per state and state
// version, and compacts each state's log once the run has left it.
//
// Service.Page reads a page of a state's lines from the chunks indexed in
// run_log_chunks, in sequence order, whole chunks at a time up to about
// 1 MiB, from the latest state version at which the run entered the state
// unless the query names one. Each line is stamped with when the worker
// began its chunk, and Sanitize strips its ANSI escape sequences on
// request. While the run is in the state a page ends before a sequence
// number not yet indexed; a chunk whose object retention removed is
// skipped and the page marked expired. A chunk the worker committed as
// lost reads as one line, Missing, and once the run has left the state so
// does each sequence number below the state's Extent not indexed, which
// counts the chunks the state's report named.
//
// Compact, lifecycle.CompactKind's handler, writes a state's chunks to
// one object, report.LogKey, through blob_objects, with Missing's line in
// place of each chunk that holds none, and commits it with the job, so
// running it again writes nothing. Service.Download signs a GET of that
// object, or answers that the state is ongoing, its compaction pending,
// or its log expired. Service.Profile signs a GET of
// the CPU profile the runner recorded for the latest execution of a
// state in run-observability, or answers that the state is ongoing or
// its profile expired. Service.Delete deletes an ended run's logs ahead
// of their retention: its chunks, each state's compacted
// log and its observability artifacts, with their records, and records
// when, after which Page, Download and Profile answer that its logs were
// deleted.
// Service.RetentionDays is how many days the run-logs store keeps a log,
// which runLogRetentionDays answers.
//
// NewPlanningReader constructs the PlanningLogs port independently of HTTP
// principals. It uses PlanningStore's minimal exact-transition projection and
// bounded chunk pages, streams raw masked objects into a bounded byte ring,
// and counts omitted complete lines without allocating an entire line. Split
// UTF-8 prefixes are omitted, while original newline endings remain. Deleted
// logs are unavailable; lost, unindexed and expired chunks contribute Missing
// placeholders and mark a partial log unavailable without discarding other
// retained text. A zero-chunk planning transition remains an available empty log.
// No private execution configuration or signed object URL enters the port.
//
// ref: decision 0008 § "Logs and artifacts"
// ref: decision 0005 § "Keys"
//
// # Input and output
//
// New takes the Store, the Objects of blob_objects, the queue a
// compaction completes its job on, the stacks domain that finds a
// deletion's stack, the database it commits on, and
// objectstore.retention.run-logs. Page, Download, Profile and Delete answer
// read.ErrNotFound for a run the caller's account lacks, and Delete
// ErrRunning for one that has not ended. Nothing is logged.
//
// # Dependencies
//
// It imports internal/kernel, platform/objectstore and its blobs,
// platform/queue, platform/tx, stacksapi and the domain's lifecycle, read
// and report packages. The domain's assembly constructs it over the pg adapter.
package logs
