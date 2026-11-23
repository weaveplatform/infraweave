package controller

import (
	"errors"
	"fmt"

	"k8s.io/client-go/dynamic"
	coordinationv1 "k8s.io/client-go/kubernetes/typed/coordination/v1"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
)

var errKubernetesConfig = errors.New("worker controller needs Kubernetes client configuration")

// Clients shares transport across the dynamic CRDs, namespaced core resources
// and Lease operations. It does not link the complete Kubernetes clientset or
// grant permissions; the service account and configured namespaces scope calls.
// Config is the API server configuration run Pods' attach and exec streams
// are opened with.
type Clients struct {
	Dynamic      dynamic.Interface
	Core         corev1.CoreV1Interface
	Coordination coordinationv1.CoordinationV1Interface
	Config       *rest.Config
}

func NewClients(config *rest.Config) (Clients, error) {
	if config == nil {
		return Clients{}, errKubernetesConfig
	}
	config = rest.CopyConfig(config)
	client, err := rest.HTTPClientFor(config)
	if err != nil {
		return Clients{}, fmt.Errorf("worker controller: Kubernetes transport: %w", err)
	}
	custom, err := dynamic.NewForConfigAndClient(config, client)
	if err != nil {
		return Clients{}, fmt.Errorf("worker controller: Kubernetes custom resources: %w", err)
	}
	core, err := corev1.NewForConfigAndClient(config, client)
	if err != nil {
		return Clients{}, fmt.Errorf("worker controller: Kubernetes core resources: %w", err)
	}
	coordination, err := coordinationv1.NewForConfigAndClient(config, client)
	if err != nil {
		return Clients{}, fmt.Errorf("worker controller: Kubernetes Leases: %w", err)
	}
	return Clients{Dynamic: custom, Core: core, Coordination: coordination, Config: config}, nil
}
