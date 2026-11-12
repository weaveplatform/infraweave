// Package suite holds the acceptance steps of features/suite, which prove
// the harness itself: each dependency tag starts its dependency, and
// nested runs of the suite over fixture features show the Docker rules
// and the evidence a failed scenario leaves.
//
// # Input and output
//
// It runs the suite's own test binary as a subprocess over the fixtures
// in testdata/, reads what the nested run wrote, and reaches the
// dependencies it starts.
//
// # Dependencies
//
// It builds on harness.World and test/deps.
package suite
