// Package phaseinputs is the model of a worker's phase-scoped inputs:
// the passthrough variables and scoped files a pool's configuration
// hands its runs, each of an exposure that names the phases it reaches.
//
// A configured variable's name may carry ReadPrefix or WritePrefix, which
// the name the run sees drops: ro_TOKEN reaches the read phases as TOKEN,
// wo_TOKEN the write phases as TOKEN, and an unprefixed name every phase.
// Phase maps a worker state to the exposure it admits beside All: the
// write phases are applying, destroying and performing, and every phase
// of a task run; the read phases are every other run's initialization
// and planning. The after_run hooks keep the exposure of the phase before
// them, and an initialization command's helper admits none.
//
// CheckVariable refuses a name a shell cannot take, an explicit exposure
// that disagrees with the prefix, and a variable the runner sets itself;
// CheckNames refuses two variables one phase would see under one name.
// CheckTargets refuses mount and scoped file targets that are not
// canonical absolute paths, that overlap Reserved paths or each other, or
// whose scoped directory a mount or a reserved path overlaps or that sits
// directly under the root. Each scoped file's directory, Dir, is one the
// executor provides empty, in memory and owned by the run's user, so the
// runner writes the phase's files there and removes the other side's.
// Check applies all of them, and the size bounds MaxFile and MaxFiles, to
// the Inputs a handoff carries.
//
// ref: decision 0008 § "Phase-scoped inputs"
// ref: decision 0013 § "Masking"
//
// # Input and output
//
// It reads nothing and writes nothing: it is types and checks.
//
// # Dependencies
//
// It imports internal/agent/wire's worker states and the standard
// library. internal/agent/config, handoff, launcher, controller and exec
// use it.
package phaseinputs
