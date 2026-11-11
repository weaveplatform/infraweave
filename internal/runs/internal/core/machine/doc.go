// Package machine is the run state machine as data: Edges, every
// transition a run may take, each with the run types that take it, the
// drivers that may take it and the guards that must hold first. Allowed
// answers whether a run of a type may move from one state to another for
// a driver, and Next lists the edges out of a state, which a run's allowed
// actions are read from. A pair of states with no edge is refused.
//
// The edges are those of docs/data/run-transitions.json, whose test reads
// the register and fails when the two differ. Creation is the edge from
// Created, the empty state; UNKNOWN and PENDING are never a target, and a
// PARSE run takes no edge.
//
// ref: decision 0008 § "The state machine is the authority"
//
// # Input and output
//
// None: it is pure, and lifecycle evaluates the guards an edge names.
//
// # Dependencies
//
// It imports internal/kernel for RunType and RunState. The runs domain's
// lifecycle and API read it.
package machine
