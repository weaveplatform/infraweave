// Package policytrigger consumes durable terminal-run policy work.
//
// # Input and output
//
// Handle evaluates the committed source and creates requested tracked runs on
// their own tracked commits. It returns only after its queue generation and
// all run/evidence writes commit together.
//
// # Effects
//
// Source receipts and flags, child creation, notifications and queue completion
// share a fenced transaction. Account-scoped fan-out serialization precedes
// source/target ownership locks. A child receives no source-owned flags.
//
// # Dependencies
//
// Owner ports from access, stacks and policies, the runs lifecycle/input owners,
// and the platform transaction/queue packages. It owns no dependency graph or
// module-release execution.
package policytrigger
