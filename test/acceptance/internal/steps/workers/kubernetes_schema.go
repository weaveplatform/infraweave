package workers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	kubeyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/weaveplatform/infraweave/internal/agent/controller"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

type schemaSteps struct {
	w                   *harness.World
	api                 dynamic.Interface
	namespace           string
	marker              string
	attempted           []string
	poolDefinition      *unstructured.Unstructured
	rejectedDefinitions []string
}

var definitionsResource = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}

func (s *schemaSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the canonical worker CRDs are established in the owned cluster$`, s.install)
	sc.Step(`^the real Kubernetes API rejects these worker pool inputs:$`, s.refuses)
	sc.Step(`^strict unknown fields are refused and non-strict fields are pruned$`, s.strictness)
	sc.Step(`^worker pool identity survives repair and becomes immutable when bound$`, s.lifecycle)
	sc.Step(`^worker resource parent and slot identities are immutable$`, s.workerIdentity)
	sc.Step(`^every pool mode accepts valid zero and maximum logical capacities$`, s.validModes)
	sc.Step(`^the real Kubernetes API refuses a nonstructural closed-object schema$`, s.nonstructural)
	sc.After(s.cleanup)
}

func (s *schemaSteps) install(ctx context.Context) error {
	cluster, err := s.w.Suite.Deps.Kubernetes(ctx)
	if err != nil {
		return err
	}
	s.api, err = dynamic.NewForConfigAndClient(&rest.Config{Host: cluster.Endpoint}, cluster.HTTPClient())
	if err != nil {
		return err
	}
	s.namespace = cluster.Namespace
	s.marker = strings.ToLower(kernel.NewULID().String())
	for _, plural := range []string{"workerpools", "workers"} {
		raw, err := os.ReadFile(filepath.Join(harness.RepoRoot, "deploy/helm/infraweave-workers/crds", plural+".yaml"))
		if err != nil {
			return err
		}
		data, err := kubeyaml.ToJSON(raw)
		if err != nil {
			return err
		}
		definition := &unstructured.Unstructured{}
		if err := definition.UnmarshalJSON(data); err != nil {
			return err
		}
		if plural == "workerpools" {
			s.poolDefinition = definition.DeepCopy()
		}
		existing, err := s.api.Resource(definitionsResource).Get(ctx, definition.GetName(), metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			existing, err = s.api.Resource(definitionsResource).Create(ctx, definition, metav1.CreateOptions{FieldValidation: metav1.FieldValidationStrict})
			if apierrors.IsAlreadyExists(err) {
				existing, err = s.api.Resource(definitionsResource).Get(ctx, definition.GetName(), metav1.GetOptions{})
			}
		}
		if err != nil {
			return fmt.Errorf("%w: install real %s CRD: %w", harness.ErrStep, plural, err)
		}
		expected, _, _ := unstructured.NestedFieldNoCopy(definition.Object, "spec", "versions")
		actual, _, _ := unstructured.NestedFieldNoCopy(existing.Object, "spec", "versions")
		// The test borrows matching installed CRDs; it never overwrites another
		// installation's schema to make its own resource cases pass.
		if !reflect.DeepEqual(expected, actual) {
			return fmt.Errorf("%w: installed %s CRD differs from its canonical source", harness.ErrStep, plural)
		}
		deadline := time.Now().Add(30 * time.Second)
		for {
			current, err := s.api.Resource(definitionsResource).Get(ctx, definition.GetName(), metav1.GetOptions{})
			if err != nil {
				return err
			}
			conditions, _, _ := unstructured.NestedSlice(current.Object, "status", "conditions")
			ready := false
			for _, condition := range conditions {
				c, _ := condition.(map[string]any)
				ready = ready || c["type"] == "Established" && c["status"] == "True"
			}
			if ready {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("%w: %s CRD did not establish", harness.ErrStep, plural)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	return nil
}

func (s *schemaSteps) validModes(ctx context.Context) error {
	for _, mode := range []string{controller.Managed, controller.Existing, controller.Public} {
		for _, capacity := range []int64{0, 2147483647} {
			input := s.pool()
			spec := input.Object["spec"].(map[string]any)
			spec["mode"], spec["capacity"] = mode, capacity
			if mode != controller.Managed {
				delete(spec, "name")
				spec["poolID"] = kernel.NewULID().String()
			}
			if mode == controller.Public {
				delete(spec, "account")
				delete(spec, "space")
			}
			created, err := s.create(ctx, input, metav1.FieldValidationStrict)
			if err != nil {
				return fmt.Errorf("%w: valid %s capacity %d: %w", harness.ErrStep, mode, capacity, err)
			}
			stored, err := s.api.Resource(controller.PoolResource).Namespace(s.namespace).Get(ctx, created.GetName(), metav1.GetOptions{})
			if err != nil {
				return err
			}
			actual, found, err := unstructured.NestedInt64(stored.Object, "spec", "capacity")
			if err != nil || !found || actual != capacity {
				return fmt.Errorf("%w: native capacity changed: %d %w", harness.ErrStep, actual, err)
			}
		}
	}
	return nil
}

func (s *schemaSteps) nonstructural(ctx context.Context) error {
	input := s.poolDefinition.DeepCopy()
	plural := "rejectedworkerpools-" + s.marker
	input.SetName(plural + "." + controller.Group)
	input.SetAnnotations(map[string]string{"io.infraweave.acceptance.owner": s.marker})
	for _, field := range []struct{ name, value string }{{"plural", plural}, {"singular", "rejectedworkerpool"}, {"kind", "RejectedWorkerPool"}, {"listKind", "RejectedWorkerPoolList"}} {
		if err := unstructured.SetNestedField(input.Object, field.value, "spec", "names", field.name); err != nil {
			return err
		}
	}
	versions := input.Object["spec"].(map[string]any)["versions"].([]any)
	root := versions[0].(map[string]any)["schema"].(map[string]any)["openAPIV3Schema"].(map[string]any)
	root["properties"].(map[string]any)["spec"].(map[string]any)["additionalProperties"] = false
	s.rejectedDefinitions = append(s.rejectedDefinitions, input.GetName())
	if _, err := s.api.Resource(definitionsResource).Create(ctx, input, metav1.CreateOptions{FieldValidation: metav1.FieldValidationStrict}); !apierrors.IsInvalid(err) {
		return fmt.Errorf("%w: nonstructural additionalProperties:false schema was not refused: %w", harness.ErrStep, err)
	}
	return nil
}

func (s *schemaSteps) pool() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": controller.Group + "/" + controller.Version,
		"kind":       "WorkerPool",
		"metadata": map[string]any{
			"name":        "schema-" + strings.ToLower(kernel.NewULID().String()),
			"namespace":   s.namespace,
			"annotations": map[string]any{"io.infraweave.acceptance.owner": s.marker},
		},
		"spec": map[string]any{
			"endpoint":       "https://infraweave.example",
			"account":        kernel.NewAccountID().String(),
			"space":          "acme",
			"mode":           controller.Managed,
			"credentialsRef": map[string]any{"name": "pool-credentials"},
			"capacity":       int64(2),
			"name":           "Managed schema input",
			"pod": map[string]any{
				"storageSize": "12Gi",
				"resources": map[string]any{
					"requests": map[string]any{"cpu": "1", "memory": "2Gi", "ephemeral-storage": "12Gi"},
					"limits":   map[string]any{"cpu": "2", "memory": "4Gi", "ephemeral-storage": "12Gi"},
				},
			},
		},
	}}
}

func (s *schemaSteps) create(ctx context.Context, input *unstructured.Unstructured, validation string) (*unstructured.Unstructured, error) {
	s.attempted = append(s.attempted, input.GetName())
	return s.api.Resource(controller.PoolResource).Namespace(s.namespace).Create(ctx, input, metav1.CreateOptions{FieldValidation: validation})
}

func (s *schemaSteps) refuses(ctx context.Context, table *godog.Table) error {
	if s.api == nil {
		return fmt.Errorf("%w: Kubernetes schema setup missing", harness.ErrStep)
	}
	for _, row := range table.Rows[1:] {
		if len(row.Cells) != 3 {
			return fmt.Errorf("%w: invalid schema case table", harness.ErrStep)
		}
		input := s.pool()
		var value any
		if err := json.Unmarshal([]byte(row.Cells[1].Value), &value); err != nil {
			return err
		}
		if err := unstructured.SetNestedField(input.Object, value, strings.Split(row.Cells[0].Value, ".")...); err != nil {
			return err
		}
		_, err := s.create(ctx, input, metav1.FieldValidationStrict)
		status, ok := err.(apierrors.APIStatus)
		wanted, parseErr := strconv.Atoi(row.Cells[2].Value)
		if parseErr != nil || !ok || int(status.Status().Code) != wanted {
			return fmt.Errorf("%w: actual Kubernetes field %s response=%w; wanted %s", harness.ErrStep, row.Cells[0].Value, err, row.Cells[2].Value)
		}
		if _, err := s.api.Resource(controller.PoolResource).Namespace(s.namespace).Get(ctx, input.GetName(), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
			return fmt.Errorf("%w: refused schema input was persisted: %w", harness.ErrStep, err)
		}
	}
	return nil
}

func (s *schemaSteps) strictness(ctx context.Context) error {
	input := s.pool()
	input.Object["spec"].(map[string]any)["rawPodSpec"] = map[string]any{"hostPID": true}
	if _, err := s.create(ctx, input, metav1.FieldValidationStrict); !apierrors.IsBadRequest(err) {
		return fmt.Errorf("%w: strict unknown input: %w", harness.ErrStep, err)
	}
	input.SetName("schema-" + strings.ToLower(kernel.NewULID().String()))
	created, err := s.create(ctx, input, metav1.FieldValidationWarn)
	if err != nil {
		return err
	}
	stored, err := s.api.Resource(controller.PoolResource).Namespace(s.namespace).Get(ctx, created.GetName(), metav1.GetOptions{})
	if err != nil {
		return err
	}
	if _, found, _ := unstructured.NestedFieldNoCopy(stored.Object, "spec", "rawPodSpec"); found {
		return fmt.Errorf("%w: non-strict unknown PodSpec survived pruning", harness.ErrStep)
	}
	return nil
}

func (s *schemaSteps) lifecycle(ctx context.Context) error {
	resource, err := s.create(ctx, s.pool(), metav1.FieldValidationStrict)
	if err != nil {
		return err
	}
	for _, change := range []struct {
		path  []string
		value any
	}{
		{[]string{"spec", "space"}, "repair-before-binding"},
		{[]string{"spec", "credentialsRef", "name"}, "repaired-credentials"},
	} {
		if err := unstructured.SetNestedField(resource.Object, change.value, change.path...); err != nil {
			return err
		}
		resource, err = s.api.Resource(controller.PoolResource).Namespace(s.namespace).Update(ctx, resource, metav1.UpdateOptions{FieldValidation: metav1.FieldValidationStrict})
		if err != nil {
			return err
		}
	}
	// This exercises native status validation only; the ULID is an explicit
	// schema fixture, not evidence that an infraweave remote pool was created.
	if err := unstructured.SetNestedField(resource.Object, kernel.NewULID().String(), "status", "poolID"); err != nil {
		return err
	}
	resource, err = s.api.Resource(controller.PoolResource).Namespace(s.namespace).UpdateStatus(ctx, resource, metav1.UpdateOptions{FieldValidation: metav1.FieldValidationStrict})
	if err != nil {
		return err
	}
	for _, change := range []struct {
		path  []string
		value any
	}{
		{[]string{"spec", "space"}, "moved-after-binding"},
		{[]string{"spec", "account"}, kernel.NewAccountID().String()},
		{[]string{"spec", "endpoint"}, "https://other.example"},
	} {
		altered := resource.DeepCopy()
		if err := unstructured.SetNestedField(altered.Object, change.value, change.path...); err != nil {
			return err
		}
		if _, err := s.api.Resource(controller.PoolResource).
			Namespace(s.namespace).
			Update(ctx, altered, metav1.UpdateOptions{FieldValidation: metav1.FieldValidationStrict}); !apierrors.IsInvalid(
			err,
		) {
			return fmt.Errorf("%w: bound identity changed at %v: %w", harness.ErrStep, change.path, err)
		}
	}
	altered := resource.DeepCopy()
	_ = unstructured.SetNestedField(altered.Object, kernel.NewULID().String(), "status", "poolID")
	if _, err := s.api.Resource(controller.PoolResource).
		Namespace(s.namespace).
		UpdateStatus(ctx, altered, metav1.UpdateOptions{FieldValidation: metav1.FieldValidationStrict}); !apierrors.IsInvalid(
		err,
	) {
		return fmt.Errorf("%w: bound pool ID changed: %w", harness.ErrStep, err)
	}
	return nil
}

func (s *schemaSteps) workerIdentity(ctx context.Context) (result error) {
	input := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": controller.Group + "/" + controller.Version,
			"kind":       "Worker",
			"metadata":   map[string]any{"name": "schema-slot-" + s.marker, "namespace": s.namespace},
			"spec":       map[string]any{"poolUID": "opaque-parent-uid", "poolID": kernel.NewULID().String(), "slot": int64(2147483647)},
		},
	}
	workers := s.api.Resource(controller.WorkerResource).Namespace(s.namespace)
	created, err := workers.Create(ctx, input, metav1.CreateOptions{FieldValidation: metav1.FieldValidationStrict})
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		uid := created.GetUID()
		result = errors.Join(result, workers.Delete(cleanup, created.GetName(), metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}))
	}()
	for _, change := range []struct {
		path  []string
		value any
	}{
		{[]string{"spec", "poolUID"}, "replacement-parent-uid"},
		{[]string{"spec", "poolID"}, kernel.NewULID().String()},
		{[]string{"spec", "slot"}, int64(0)},
	} {
		altered := created.DeepCopy()
		if err := unstructured.SetNestedField(altered.Object, change.value, change.path...); err != nil {
			return err
		}
		if _, err := workers.Update(ctx, altered, metav1.UpdateOptions{FieldValidation: metav1.FieldValidationStrict}); !apierrors.IsInvalid(err) {
			return fmt.Errorf("%w: Worker identity changed: %w", harness.ErrStep, err)
		}
	}
	return nil
}

func (s *schemaSteps) cleanup(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
	if s.api == nil {
		return ctx, nil
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	var result error
	resources := s.api.Resource(controller.PoolResource).Namespace(s.namespace)
	for _, name := range s.attempted {
		resource, err := resources.Get(cleanup, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		if resource.GetAnnotations()["io.infraweave.acceptance.owner"] != s.marker {
			result = errors.Join(result, fmt.Errorf("%w: refusing foreign schema resource cleanup", harness.ErrStep))
			continue
		}
		uid := types.UID(resource.GetUID())
		if err := resources.Delete(cleanup, name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil {
			result = errors.Join(result, err)
			continue
		}
		if _, err := resources.Get(cleanup, name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
			result = errors.Join(result, fmt.Errorf("%w: schema resource remains after cleanup: %w", harness.ErrStep, err))
		}
	}
	definitions := s.api.Resource(definitionsResource)
	for _, name := range s.rejectedDefinitions {
		definition, err := definitions.Get(cleanup, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		if definition.GetAnnotations()["io.infraweave.acceptance.owner"] != s.marker {
			result = errors.Join(result, fmt.Errorf("%w: refusing foreign CRD cleanup", harness.ErrStep))
			continue
		}
		uid := definition.GetUID()
		result = errors.Join(result, definitions.Delete(cleanup, name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}))
	}
	return ctx, result
}
