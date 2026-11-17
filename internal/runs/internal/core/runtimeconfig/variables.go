package runtimeconfig

import (
	"fmt"
	"strings"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// reserved are the prefixes of the variables infraweave sets for a run,
// which no override replaces: the runner's TF_VAR_infraweave_* and the
// launcher's INFRAWEAVE_*, such as INFRAWEAVE_API_TOKEN.
var reserved = []string{"TF_VAR_infraweave_", "INFRAWEAVE_"}

// CheckVariables checks run-local variable names and values using the same
// rules for proposal overrides and runtime configuration. Errors point below
// pointer, which identifies the caller's variable list.
//
// ref: decision 0008 § "The run specification and its secrets"
func CheckVariables(ovs []Variable, pointer string) error {
	var errs []kernel.FieldError
	seen := make(map[string]bool, len(ovs))
	for i, o := range ovs {
		at := fmt.Sprintf("%s/%d", pointer, i)
		refuse := func(field, reason, message string) {
			errs = append(errs, kernel.FieldError{
				Pointer: at + field, Reason: reason, Message: message,
			})
		}
		switch {
		case o.Key == "":
			refuse("/key", kernel.ReasonRequired, "An override names its variable.")
		case strings.ContainsRune(o.Key, 0):
			refuse(
				"/key",
				kernel.ReasonRule,
				"An environment variable's name holds no NUL character.",
			)
		case strings.Contains(o.Key, "="):
			refuse("/key", kernel.ReasonRule, "An environment variable's name holds no =.")
		case isReserved(o.Key):
			refuse("/key", kernel.ReasonRule,
				fmt.Sprintf("infraweave sets %s for the run, so no override replaces it.", o.Key))
		case seen[o.Key]:
			refuse("/key", kernel.ReasonDuplicate,
				fmt.Sprintf("The variable %s is overridden twice.", o.Key))
		}
		seen[o.Key] = true
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
	if name == "TF_APPEND_USER_AGENT" {
		return true
	}
	for _, p := range reserved {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}
