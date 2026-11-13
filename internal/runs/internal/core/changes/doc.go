// Package changes reads the changes a run's phases made, which the
// runChangesV2 and runChangesV3 operations answer. A run's worker uploads
// changes.json after planning, sealed with the run's artifact key; the
// service opens the one the run recorded last while planning, which
// blob_objects unseals, and decodes it with internal/iac/plan. Only the
// plan records changes yet, so it is a run's latest phase and an apply or
// drift detection has none. A run that has not planned has no changes;
// one whose document retention removed answers ErrExpired.
//
// Service.Resources serves Terraform and OpenTofu stacks alone, refusing
// another vendor's run as a *kernel.Invalid; Service.Entities serves
// every stack.
//
// ref: decision 0008 § "Phases, reports and the plan decision"
// ref: decision 0013 § "Sealed objects"
//
// # Input and output
//
// New takes the Store, the Objects of blob_objects and the stacks
// domain's settings. Both reads answer read.ErrNotFound for a run the
// caller's account lacks, and the decoder's error for a document it
// refuses. Nothing is logged.
//
// # Dependencies
//
// It imports internal/iac and its plan, internal/kernel,
// platform/objectstore, stacksapi and the domain's lifecycle and read
// packages. The domain's assembly constructs it over the pg adapter.
package changes
