package trigger

import (
	"fmt"
	"strings"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
)

// reserved are the prefixes of the variables infraweave sets for a run,
// which no override replaces: the runner's TF_VAR_infraweave_* and the
// launcher's INFRAWEAVE_*, such as INFRAWEAVE_API_TOKEN.
var reserved = []string{"TF_VAR_infraweave_", "INFRAWEAVE_"}

// CheckOverrides refuses, as a *kernel.Invalid with a pointer under
// /environmentVarsOverrides/<i>, each override whose name is empty, holds
// = or NUL, is given twice or is one infraweave sets, and each value that
// holds NUL: a context's environment variable follows the same rules.
// The request body's limit bounds their count and size, as it does a
// context's.
//
// ref: decision 0008 § "The run specification and its secrets"
func CheckOverrides(ovs []lifecycle.Override) error {
	var errs []kernel.FieldError
	seen := make(map[string]bool, len(ovs))
	for i, o := range ovs {
		at := fmt.Sprintf("/environmentVarsOverrides/%d", i)
		refuse := func(field, reason, message string) {
			errs = append(errs, kernel.FieldError{
				Pointer: at + field, Reason: reason, Message: message,
			})
		}
		switch {
		case o.Name == "":
			refuse("/key", kernel.ReasonRequired, "An override names its variable.")
		case strings.ContainsRune(o.Name, 0):
			refuse(
				"/key",
				kernel.ReasonRule,
				"An environment variable's name holds no NUL character.",
			)
		case strings.Contains(o.Name, "="):
			refuse("/key", kernel.ReasonRule, "An environment variable's name holds no =.")
		case isReserved(o.Name):
			refuse("/key", kernel.ReasonRule,
				fmt.Sprintf("infraweave sets %s for the run, so no override replaces it.", o.Name))
		case seen[o.Name]:
			refuse("/key", kernel.ReasonDuplicate,
				fmt.Sprintf("The variable %s is overridden twice.", o.Name))
		}
		seen[o.Name] = true
		if strings.ContainsRune(o.Value, 0) {
			refuse("/value", kernel.ReasonRule,
				"An environment variable's value holds no NUL character.")
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return &kernel.Invalid{Detail: "The environment variable overrides are refused.", Errors: errs}
}

func isReserved(name string) bool {
	for _, p := range reserved {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}
