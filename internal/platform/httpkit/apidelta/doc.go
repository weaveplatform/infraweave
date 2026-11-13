// Package apidelta writes a run's plan delta, a kernel.PlanDelta, as the
// API's Delta, for every domain that answers one: a run answers its own
// plan's, and a stack the plan of the run blocking it while that run
// waits for the plan's confirmation. Each count is clamped to the API's
// int32, and the counts of Ansible's tasks are zero, since no
// Terraform-family plan has them.
//
// # Input and output
//
// None: it converts values in memory.
//
// # Dependencies
//
// It imports kernel and platform/gen/api. The HTTP adapters of the runs
// and stacks domains import it.
package apidelta
