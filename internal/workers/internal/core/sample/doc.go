// Package sample keeps the worker pools' samples. Service's Sample is
// worker.sample's pass: each minute it writes one sample of each private
// pool of every account, its busy and its registered workers and the
// runs waiting for one, which the workersapi.Scheduler that Bind binds
// counts, at the minute, so a second pass in the same minute writes
// nothing; then it deletes the samples older than
// worker.samples.retention, a bounded number a pass. SamplesBetween
// answers a pool's samples in a window, oldest first, which the usage
// assessment and the usage read models read through workersapi.Samples.
//
// ref: decision 0008 § "Pool samples"
//
// # Input and output
//
// New takes the Store, the retention and a clock; Bind takes the runs
// domain's scheduler.
//
// # Dependencies
//
// It imports the workers api package and internal/kernel. The domain's
// assembly constructs it over the pg adapter.
package sample
