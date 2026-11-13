// Package runlog is a run's log in the runner: every line the run's
// phases and hooks write is masked in the container, before it leaves,
// written to standard output, which the launcher reads, and uploaded in
// chunks that the run's log readers tail.
//
// New starts a Log over a pipe. The session's shell writes its output to
// File, the pipe's write end, and the runner writes its own lines through
// Write; one goroutine reads the pipe a line at a time, cut at
// mask.MaxLine. Each line is masked with the run's Masker, which an
// ::add-mask v1 v2 line grows by its words before the line itself is
// masked, as Add grows it by the values of the variables the launcher's
// mask list names. Begin opens a state version: its masked lines collect
// in chunks of at most MaxChunk bytes, each cut Cut after its first line
// or when the next line would not fit. Another goroutine uploads the
// chunks in order: it declares each through upload-urls as a log-chunk
// named by its sequence number, PUTs it to the pre-signed URL and commits
// it through log-chunks, retrying a step the worker API or the store
// could not take for now and dropping, with a warning, a chunk it cannot
// send.
//
// End writes a barrier, a line holding a nonce of the runner's, to the
// pipe and waits for the reader to read it, so every line a step wrote
// before it is in a chunk; then it cuts the last chunk and waits for
// every chunk to be committed. The runner calls it before it reports a
// state's end, so a reader of a finished state sees every line. Close
// ends the log as End does and stops both goroutines.
//
// ref: decision 0008 § "Logs and artifacts"
// ref: decision 0013 § "Masking"
//
// # Input and output
//
// It reads what is written to the pipe, writes masked lines to Deps.Out,
// and calls the worker API's upload-urls and log-chunks and the store's
// pre-signed PUTs through Deps.API. A chunk retried or dropped is logged
// at warn with the run, state version and sequence number; a line is
// never logged.
//
// # Dependencies
//
// It imports internal/agent/client and wire, platform/mask and the
// standard library. internal/agent/exec starts one per run.
package runlog
