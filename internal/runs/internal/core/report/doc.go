// Package report is the runs domain's side of a claimed run's calls
// after its handover: the worker API's reports, upload-urls, log-chunks,
// artifacts and workspace. Service finds the run by its ID alone, as a
// run token names it, locks its row, and refuses one that is not on a
// private pool's worker.
//
// A report moves the run through the edges a worker's report drives: a
// started report of the state the run's preparation leads to ends the
// handover (T10, T32, T35), a succeeded report enters the next phase or
// ends the run, and failed and stopped reports end it. The answer is the
// next step, execute, pause or exit, with the run's state version, which
// the next report names; a report that names an older state version is
// refused, unless it repeats the one that took the run's last transition.
// A succeeded report of PLANNING is the plan decision's, which the
// service does not take.
//
// Uploads are keyed <account>/<run>/<state>-<stateVersion>/<name> in
// their kind's store, recorded pending in blob_objects before their PUT
// URLs are signed, and made live when the worker indexes a log chunk in
// run_log_chunks or records another object in run_artifacts. A run
// artifact is sealed by the runner under a content key derived from the
// run's artifact key and the object's key, which its row holds sealed.
// PurgeObjects deletes a deleted account's chunks and records with their
// objects.
//
// ref: decision 0008 § "Phases, reports and the plan decision"
// ref: decision 0008 § "Logs and artifacts"
// ref: decision 0013 § "Sealed objects"
//
// # Input and output
//
// New takes the Store, the Objects of blob_objects, the lifecycle, and
// the database a purge opens transactions on. Each call answers what
// workersapi.Reports says, with workersapi's refusals and a
// *kernel.Invalid for a declaration or upload it refuses. Nothing is
// logged.
//
// # Dependencies
//
// It imports the workers api package, internal/kernel, platform/crypt,
// platform/objectstore and its blobs, platform/seal, platform/tx and the
// domain's lifecycle and machine packages. The domain's assembly
// constructs it over the pg adapter.
package report
