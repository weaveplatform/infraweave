package kernel

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// WorkerOwner identifies a WorkerPool resource independently of its display
// name. The remote server scopes the installation; cluster, namespace and UID
// prevent a replacement resource from adopting a surviving pool.
type WorkerOwner struct {
	ClusterID   string `json:"clusterID"` //nolint:tagliatelle // the signed protocol uses clusterID
	Namespace   string `json:"namespace"`
	ResourceUID string `json:"resourceUID"` //nolint:tagliatelle // Kubernetes UIDs are opaque, not ULIDs
}

// CheckWorkerOwner validates the bounded identity shared by controller requests
// and signed pool proofs. It never normalizes an identity before comparison.
func CheckWorkerOwner(at string, owner WorkerOwner) []FieldError {
	var faults []FieldError
	for _, field := range []struct{ name, value string }{
		{"clusterID", owner.ClusterID}, {"namespace", owner.Namespace},
	} {
		if !workerOwnerLabel(field.value) {
			faults = append(faults, FieldError{Pointer: at + "/" + field.name, Reason: ReasonPattern, Message: "Use a 1..63-character lowercase DNS label."})
		}
	}
	if !utf8.ValidString(owner.ResourceUID) || strings.TrimSpace(owner.ResourceUID) == "" || utf8.RuneCountInString(owner.ResourceUID) > 128 {
		faults = append(faults, FieldError{Pointer: at + "/resourceUID", Reason: ReasonRule, Message: "Use a nonempty UTF-8 resource UID of at most 128 characters."})
	} else {
		for _, ch := range owner.ResourceUID {
			if unicode.IsControl(ch) {
				faults = append(faults, FieldError{Pointer: at + "/resourceUID", Reason: ReasonPattern, Message: "A resource UID contains no control characters."})
				break
			}
		}
	}
	return faults
}

func workerOwnerLabel(value string) bool {
	if len(value) < 1 || len(value) > 63 || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, ch := range value {
		if ch != '-' && (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') {
			return false
		}
	}
	return true
}
