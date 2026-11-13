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
// skipped and the page marked expired.
//
// Compact, lifecycle.CompactKind's handler, writes a state's chunks to
// one object, report.LogKey, through blob_objects, and commits it with
// the job, so running it again writes nothing. Service.Download signs a
// GET of that object, or answers that the state is ongoing, its
// compaction pending, or its log expired.
//
// ref: decision 0008 § "Logs and artifacts"
// ref: decision 0005 § "Keys"
//
// # Input and output
//
// New takes the Store, the Objects of blob_objects, the queue a
// compaction completes its job on, and the database it commits on. Page
// and Download answer read.ErrNotFound for a run the caller's account
// lacks. Nothing is logged.
//
// # Dependencies
//
// It imports internal/kernel, platform/objectstore and its blobs,
// platform/queue, platform/tx and the domain's lifecycle, read and report
// packages. The domain's assembly constructs it over the pg adapter.
package logs
