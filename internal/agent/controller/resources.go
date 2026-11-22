package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

const (
	Group     = "workers.infraweave.io"
	Version   = "v1alpha1"
	Finalizer = Group + "/cleanup"
	Managed   = "Managed"
	Existing  = "Existing"
	Public    = "Public"
)

var (
	PoolResource   = schema.GroupVersionResource{Group: Group, Version: Version, Resource: "workerpools"}
	WorkerResource = schema.GroupVersionResource{Group: Group, Version: Version, Resource: "workers"}
)

// WorkerPool contains references and public observations, never opened
// management credentials, pool private keys or worker-session/run tokens.
type WorkerPool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`
	Spec              PoolSpec   `json:"spec"`
	Status            PoolStatus `json:"status,omitempty"`
}

type PoolSpec struct {
	Endpoint                string                      `json:"endpoint"`
	Account                 *string                     `json:"account,omitempty"`
	Space                   *string                     `json:"space,omitempty"`
	Mode                    string                      `json:"mode"`
	CredentialsRef          corev1.LocalObjectReference `json:"credentialsRef"`
	PoolID                  *string                     `json:"poolID,omitempty"` //nolint:tagliatelle // the protocol uses poolID
	Capacity                int32                       `json:"capacity"`
	Name                    *string                     `json:"name,omitempty"`
	Description             *string                     `json:"description,omitempty"`
	Labels                  *[]string                   `json:"labels,omitempty"`
	PreparingTimeoutSeconds *int32                      `json:"preparingTimeoutSeconds,omitempty"`
	DriftDetectionRunLimit  *int32                      `json:"driftDetectionRunLimit,omitempty"`
	Pod                     PodSettings                 `json:"pod"`
	Environment             []Environment               `json:"environment,omitempty"`
	Mounts                  []Mount                     `json:"mounts,omitempty"`
}

// PodSettings exposes only placement and bounded resource/storage choices.
// Security context, commands, containers and service accounts are fixed by the
// executor and cannot be supplied as an arbitrary PodSpec.
type PodSettings struct {
	Resources        Resources                     `json:"resources"`
	HelperResources  *Resources                    `json:"helperResources,omitempty"`
	StorageSize      resource.Quantity             `json:"storageSize"`
	NodeSelector     map[string]string             `json:"nodeSelector,omitempty"`
	Tolerations      []corev1.Toleration           `json:"tolerations,omitempty"`
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`
}

type Resources struct {
	Requests corev1.ResourceList `json:"requests"`
	Limits   corev1.ResourceList `json:"limits"`
}

type Environment struct {
	Name     string `json:"name"`
	Exposure string `json:"exposure"`
}

// Mount resolves a same-namespace Secret/ConfigMap key or PVC. ReadOnly is
// explicit and mandatory; no node/controller filesystem path is a source.
type Mount struct {
	Kind     string  `json:"kind"`
	Name     string  `json:"name"`
	Key      *string `json:"key,omitempty"`
	Target   string  `json:"target"`
	Exposure string  `json:"exposure"`
	ReadOnly bool    `json:"readOnly"`
}

type PoolStatus struct {
	ObservedGeneration int64               `json:"observedGeneration,omitempty"`
	PoolID             string              `json:"poolID,omitempty"` //nolint:tagliatelle // the protocol uses poolID
	Owner              *kernel.WorkerOwner `json:"owner,omitempty"`
	Desired            int32               `json:"desired,omitempty"`
	Registered         int32               `json:"registered,omitempty"`
	Ready              int32               `json:"ready,omitempty"`
	Busy               int32               `json:"busy,omitempty"`
	Retiring           int32               `json:"retiring,omitempty"`
	Conditions         []metav1.Condition  `json:"conditions,omitempty"`
}

type Worker struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`
	Spec              WorkerSpec   `json:"spec"`
	Status            WorkerStatus `json:"status,omitempty"`
}

type WorkerSpec struct {
	PoolUID string `json:"poolUID"` //nolint:tagliatelle // Kubernetes UIDs are opaque
	PoolID  string `json:"poolID"`  //nolint:tagliatelle // the protocol uses poolID
	Slot    int32  `json:"slot"`
}

type WorkerStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	WorkerID           string             `json:"workerID,omitempty"` //nolint:tagliatelle // the protocol uses workerID
	Assignment         *AssignmentStatus  `json:"assignment,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
}

// AssignmentStatus records durable identity/acknowledgement, not a handoff's
// secret contents. A stale replica cannot infer ownership from a Pod name.
type AssignmentStatus struct {
	RunID               string       `json:"runID"`            //nolint:tagliatelle // the protocol uses runID
	PodUID              string       `json:"podUID,omitempty"` //nolint:tagliatelle // Kubernetes UIDs are opaque
	PodName             string       `json:"podName,omitempty"`
	State               string       `json:"state"`
	StateVersion        int64        `json:"stateVersion"`
	PreparationDeadline *metav1.Time `json:"preparationDeadline,omitempty"`
	RunDeadline         *metav1.Time `json:"runDeadline,omitempty"`
	LogDeadline         *metav1.Time `json:"logDeadline,omitempty"`
	StartedAt           *metav1.Time `json:"startedAt,omitempty"`
	HandoffPhase        string       `json:"handoffPhase,omitempty"`
	ExitCode            *int32       `json:"exitCode,omitempty"`
	ExitReason          string       `json:"exitReason,omitempty"`
	HandoffAcknowledged bool         `json:"handoffAcknowledged"`
}

func (p WorkerPool) Owner(clusterID string) kernel.WorkerOwner {
	return kernel.WorkerOwner{ClusterID: clusterID, Namespace: p.Namespace, ResourceUID: string(p.UID)}
}

// SlotName uses the parent UID so a replacement resource of the same display
// name cannot adopt its predecessor's surviving child. The full slot number
// remains visible; its maximum value still fits a Kubernetes DNS label.
func SlotName(poolUID string, slot int32) string {
	hash := sha256.Sum256([]byte(poolUID))
	return "iw-worker-" + hex.EncodeToString(hash[:16]) + "-" + strconv.FormatInt(int64(slot), 10)
}

// DeepCopy is s with no storage shared with it.
func (s PoolStatus) DeepCopy() *PoolStatus {
	out := s
	if s.Owner != nil {
		owner := *s.Owner
		out.Owner = &owner
	}
	out.Conditions = slices.Clone(s.Conditions)
	return &out
}

// DeepCopy is s with no storage shared with it.
func (s WorkerStatus) DeepCopy() *WorkerStatus {
	out := s
	if s.Assignment != nil {
		a := *s.Assignment
		out.Assignment = &a
	}
	out.Conditions = slices.Clone(s.Conditions)
	return &out
}
