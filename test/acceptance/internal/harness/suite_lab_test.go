package harness

import (
	"bytes"
	"context"
	"net"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

func TestUnitLiveLabScenariosOwnTheSharedIngressSequentially(t *testing.T) {
	previous := runtime.GOMAXPROCS(2)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
	t.Run("parallel control collides", func(t *testing.T) {
		status, collision, output := sharedIngressScenarios(t, featureConcurrency("core"))
		if status == 0 || !collision {
			t.Fatalf("parallel control did not collide on its actual listener: status=%d collision=%v\n%s", status, collision, output)
		}
	})
	t.Run("live lab retains exclusive ingress", func(t *testing.T) {
		status, collision, output := sharedIngressScenarios(t, featureConcurrency("vcs-lab"))
		if status != 0 || collision {
			t.Fatalf("live lab shared its ingress between active scenarios: status=%d collision=%v\n%s", status, collision, output)
		}
	})
}

func sharedIngressScenarios(t *testing.T, concurrency int) (int, bool, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	first, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	address := first.Addr().String()
	bound, secondAttempted := make(chan struct{}), make(chan struct{})
	var owners atomic.Int32
	var collided atomic.Bool
	var output bytes.Buffer
	status := godog.TestSuite{ScenarioInitializer: func(sc *godog.ScenarioContext) {
		var listener net.Listener
		var firstOwner bool
		sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
			firstOwner = owners.Add(1) == 1
			if firstOwner {
				listener = first
				close(bound)
				return ctx, nil
			}
			select {
			case <-bound:
			case <-ctx.Done():
				return ctx, ctx.Err()
			}
			defer close(secondAttempted)
			var err error
			listener, err = (&net.ListenConfig{}).Listen(ctx, "tcp", address)
			collided.Store(err != nil)
			return ctx, err
		})
		sc.Step(`^the ingress belongs to this scenario$`, func(ctx context.Context) error {
			if firstOwner && concurrency > 1 {
				select {
				case <-secondAttempted:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		})
		sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
			if listener != nil {
				return ctx, listener.Close()
			}
			return ctx, nil
		})
	}, Options: &godog.Options{
		Format: "progress", Output: &output, DefaultContext: ctx, Strict: true, Concurrency: concurrency,
		FeatureContents: []godog.Feature{{Name: "shared-ingress.feature", Contents: []byte(`Feature: Exclusive live-lab ingress
  Scenario: first owner
    Given the ingress belongs to this scenario
  Scenario: next owner
    Given the ingress belongs to this scenario
`)}},
	}}.Run()
	if owners.Load() != 2 {
		t.Fatalf("ingress regression ran %d owners, want two", owners.Load())
	}
	return status, collided.Load(), output.String()
}
