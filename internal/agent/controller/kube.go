package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

// PoolLabel names, on each Worker resource, the UID of the WorkerPool that
// owns it, so a pool lists its workers without reading the namespace's.
const PoolLabel = Group + "/pool-uid"

// strict refuses unknown fields on every write rather than letting the API
// server prune them.
const strict = metav1.FieldValidationStrict

func decode[T any](u *unstructured.Unstructured) (T, error) {
	var out T
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &out); err != nil {
		return out, fmt.Errorf("worker controller: decode %s %s/%s: %w", u.GetKind(), u.GetNamespace(), u.GetName(), err)
	}
	return out, nil
}

func encode(kind string, obj any) (*unstructured.Unstructured, error) {
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, fmt.Errorf("worker controller: encode %s: %w", kind, err)
	}
	u := &unstructured.Unstructured{Object: m}
	u.SetAPIVersion(Group + "/" + Version)
	u.SetKind(kind)
	return u, nil
}

func (c *Controller) pools(ctx context.Context, namespace string) ([]WorkerPool, error) {
	list, err := c.kube.Dynamic.Resource(PoolResource).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("worker controller: list worker pools in %s: %w", namespace, err)
	}
	out := make([]WorkerPool, 0, len(list.Items))
	for i := range list.Items {
		wp, err := decode[WorkerPool](&list.Items[i])
		if err != nil {
			return nil, err
		}
		out = append(out, wp)
	}
	return out, nil
}

func (c *Controller) workers(ctx context.Context, wp WorkerPool) ([]Worker, error) {
	list, err := c.kube.Dynamic.Resource(WorkerResource).Namespace(wp.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: PoolLabel + "=" + string(wp.UID),
	})
	if err != nil {
		return nil, fmt.Errorf("worker controller: list workers of %s/%s: %w", wp.Namespace, wp.Name, err)
	}
	out := make([]Worker, 0, len(list.Items))
	for i := range list.Items {
		w, err := decode[Worker](&list.Items[i])
		if err != nil {
			return nil, err
		}
		if w.Spec.PoolUID == string(wp.UID) {
			out = append(out, w)
		}
	}
	return out, nil
}

// setFinalizers replaces the finalizers of the resource meta names with
// finalizers. While meta has finalizers the patch first tests that they
// are still the ones read, and otherwise it carries meta's
// resourceVersion; either way a concurrent change answers a conflict the
// next pass rereads. Only metadata is written: the spec a user owns never
// round-trips through the controller. A resource that is gone has no
// finalizer left to remove.
func (c *Controller) setFinalizers(ctx context.Context, gvr schema.GroupVersionResource, meta metav1.ObjectMeta, finalizers []string) error {
	if finalizers == nil {
		finalizers = []string{}
	}
	kind, body := types.MergePatchType, any(map[string]any{"metadata": map[string]any{
		"resourceVersion": meta.ResourceVersion, "finalizers": finalizers,
	}})
	if len(meta.Finalizers) > 0 {
		kind, body = types.JSONPatchType, []map[string]any{
			{"op": "test", "path": "/metadata/finalizers", "value": meta.Finalizers},
			{"op": "replace", "path": "/metadata/finalizers", "value": finalizers},
		}
	}
	patch, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("worker controller: finalizer patch: %w", err)
	}
	_, err = c.kube.Dynamic.Resource(gvr).Namespace(meta.Namespace).Patch(ctx, meta.Name, kind, patch, metav1.PatchOptions{FieldValidation: strict})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("worker controller: set finalizers of %s %s/%s: %w", gvr.Resource, meta.Namespace, meta.Name, err)
	}
	return nil
}

// setStatus replaces the status of the resource meta names. A JSON patch
// replaces it whole, so a count that falls to zero, which a merge patch
// would omit, is written too.
func (c *Controller) setStatus(ctx context.Context, gvr schema.GroupVersionResource, meta metav1.ObjectMeta, status any) error {
	patch, err := json.Marshal([]map[string]any{{"op": "add", "path": "/status", "value": status}})
	if err != nil {
		return fmt.Errorf("worker controller: status patch: %w", err)
	}
	_, err = c.kube.Dynamic.Resource(gvr).Namespace(meta.Namespace).Patch(ctx, meta.Name, types.JSONPatchType, patch, metav1.PatchOptions{FieldValidation: strict}, "status")
	if err != nil {
		return fmt.Errorf("worker controller: update status of %s %s/%s: %w", gvr.Resource, meta.Namespace, meta.Name, err)
	}
	return nil
}

func (c *Controller) createWorker(ctx context.Context, w Worker) (Worker, error) {
	u, err := encode("Worker", &w)
	if err != nil {
		return w, err
	}
	out, err := c.kube.Dynamic.Resource(WorkerResource).Namespace(w.Namespace).Create(ctx, u, metav1.CreateOptions{FieldValidation: strict})
	if apierrors.IsAlreadyExists(err) {
		out, err = c.kube.Dynamic.Resource(WorkerResource).Namespace(w.Namespace).Get(ctx, w.Name, metav1.GetOptions{})
	}
	if err != nil {
		return w, fmt.Errorf("worker controller: create worker %s/%s: %w", w.Namespace, w.Name, err)
	}
	return decode[Worker](out)
}

// deleteWorker deletes the Worker w, only while it is the same object, so
// a replacement of the same name is never deleted in its place.
func (c *Controller) deleteWorker(ctx context.Context, w Worker) error {
	uid := w.UID
	err := c.kube.Dynamic.Resource(WorkerResource).Namespace(w.Namespace).Delete(ctx, w.Name, metav1.DeleteOptions{
		Preconditions: &metav1.Preconditions{UID: &uid},
	})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("worker controller: delete worker %s/%s: %w", w.Namespace, w.Name, err)
	}
	return nil
}

func hasFinalizer(meta metav1.ObjectMeta) bool { return slices.Contains(meta.Finalizers, Finalizer) }

func withoutFinalizer(meta metav1.ObjectMeta) []string {
	return slices.DeleteFunc(slices.Clone(meta.Finalizers), func(f string) bool { return f == Finalizer })
}

// newWorker is the Worker resource of wp's slot, owned by wp so that
// Kubernetes removes it with its pool once its finalizer is released.
func newWorker(wp WorkerPool, pool string, slot int32) Worker {
	yes := true
	return Worker{
		ObjectMeta: metav1.ObjectMeta{
			Name: SlotName(string(wp.UID), slot), Namespace: wp.Namespace,
			Labels:     map[string]string{PoolLabel: string(wp.UID)},
			Finalizers: []string{Finalizer},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: Group + "/" + Version, Kind: "WorkerPool", Name: wp.Name, UID: types.UID(string(wp.UID)),
				Controller: &yes, BlockOwnerDeletion: &yes,
			}},
		},
		Spec: WorkerSpec{PoolUID: string(wp.UID), PoolID: pool, Slot: slot},
	}
}
