package lifecycle

import "github.com/weaveplatform/infraweave/internal/kernel"

// Validate the selected pool while Create holds the stack, before reuse,
// admission callbacks or writes. Public claims retain their own exclusion for
// legacy or independently persisted priority/drift candidates.
func checkPublicCreation(pool *kernel.ULID, in NewRun) error {
	if pool != nil {
		return nil
	}
	var fields []kernel.FieldError
	if in.Priority != "" && in.Priority != kernel.RunPriorityNormal {
		fields = append(fields, kernel.FieldError{
			Pointer: "/priority", Reason: kernel.ReasonRule,
			Message: "Public worker pool runs use normal priority and FIFO ordering.",
		})
	}
	if in.DriftDetection {
		fields = append(fields, kernel.FieldError{
			Pointer: "/driftDetection", Reason: kernel.ReasonRule,
			Message: "Drift detection requires a private worker pool.",
		})
	}
	if len(fields) == 0 {
		return nil
	}
	return &kernel.Invalid{Detail: "The selected public worker pool does not support this execution configuration.", Errors: fields}
}
