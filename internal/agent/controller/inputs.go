package controller

import (
	"context"
	"errors"
	"fmt"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/weaveplatform/infraweave/internal/agent/executor"
	"github.com/weaveplatform/infraweave/internal/agent/launcher"
	"github.com/weaveplatform/infraweave/internal/agent/phaseinputs"
)

// poolInputs are a slot's launcher.Inputs: its WorkerPool's environment
// and mounts, which each assignment resolves afresh in the pool's
// namespace. A variable's value is the key of a Secret or ConfigMap, and
// sensitive when a Secret holds it; a mount of all exposure is a
// read-only volume of the run Pod, and a read or write mount a Secret's
// or ConfigMap's key the controller reads and the handoff carries.
type poolInputs struct {
	core      corev1.CoreV1Interface
	namespace string

	mu          sync.Mutex
	environment []Environment
	mounts      []Mount
}

var _ launcher.Inputs = (*poolInputs)(nil)

// set replaces the spec the next assignment resolves.
func (p *poolInputs) set(spec PoolSpec) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.environment, p.mounts = spec.Environment, spec.Mounts
}

// errObject marks an object or key the pool references that the
// namespace does not hold, and errKind a reference of a kind that cannot
// supply it.
var (
	errObject = errors.New("the namespace holds no such object or key")
	errKind   = errors.New("a claim is mounted in every phase; read and write mounts are Secret or ConfigMap keys")
)

// Resolve reads the pool's environment and scoped mounts from its
// namespace. An error names the reference, never a value.
func (p *poolInputs) Resolve(ctx context.Context) (launcher.Resolved, error) {
	p.mu.Lock()
	environment, mounts := p.environment, p.mounts
	p.mu.Unlock()
	var r launcher.Resolved
	names := make([]string, 0, len(environment))
	for _, e := range environment {
		name, exposure, err := phaseinputs.CheckVariable(e.Name, e.Exposure)
		if err != nil {
			return launcher.Resolved{}, fmt.Errorf("environment %s: %w", e.Name, err)
		}
		names = append(names, e.Name)
		value, err := p.read(ctx, e.From.Kind, e.From.Name, e.From.Key)
		if err != nil {
			return launcher.Resolved{}, fmt.Errorf("environment %s: %w", e.Name, err)
		}
		r.Inputs.Variables = append(r.Inputs.Variables, phaseinputs.Variable{
			Name: name, Exposure: exposure, Value: string(value), Sensitive: e.From.Kind == executor.Secret,
		})
	}
	if err := phaseinputs.CheckNames(names); err != nil {
		return launcher.Resolved{}, fmt.Errorf("environment: %w", err)
	}
	for _, m := range mounts {
		exposure, err := phaseinputs.ParseExposure(m.Exposure)
		if err != nil {
			return launcher.Resolved{}, fmt.Errorf("mount %s: %w", m.Target, err)
		}
		key := ""
		if m.Key != nil {
			key = *m.Key
		}
		if exposure == phaseinputs.All {
			r.Volumes = append(r.Volumes, executor.Volume{Kind: m.Kind, Name: m.Name, Key: key, Target: m.Target})
			continue
		}
		if m.Kind != executor.Secret && m.Kind != executor.ConfigMap {
			return launcher.Resolved{}, fmt.Errorf("mount %s: %w", m.Target, errKind)
		}
		content, err := p.read(ctx, m.Kind, m.Name, key)
		if err != nil {
			return launcher.Resolved{}, fmt.Errorf("mount %s: %w", m.Target, err)
		}
		r.Inputs.Files = append(r.Inputs.Files, phaseinputs.File{
			Target: m.Target, Exposure: exposure, Content: content, Sensitive: m.Kind == executor.Secret,
		})
	}
	return r, nil
}

// read answers the key of the Secret or ConfigMap name in the pool's
// namespace.
func (p *poolInputs) read(ctx context.Context, kind, name, key string) ([]byte, error) {
	switch kind {
	case executor.Secret:
		s, err := p.core.Secrets(p.namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("the Secret %s: %w", name, err)
		}
		if v, ok := s.Data[key]; ok {
			return v, nil
		}
		if v, ok := s.StringData[key]; ok {
			return []byte(v), nil
		}
	case executor.ConfigMap:
		c, err := p.core.ConfigMaps(p.namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("the ConfigMap %s: %w", name, err)
		}
		if v, ok := c.Data[key]; ok {
			return []byte(v), nil
		}
		if v, ok := c.BinaryData[key]; ok {
			return v, nil
		}
	default:
		return nil, fmt.Errorf("%w: the %s %s", errKind, kind, name)
	}
	return nil, fmt.Errorf("%w: the %s %s key %s", errObject, kind, name, key)
}
