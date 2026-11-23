package harness

import (
	"bytes"
	"context"
	"errors"
	"net"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/deps"
)

func TestUnitLiveLabScenariosOwnTheSharedIngressSequentially(t *testing.T) {
	previous := runtime.GOMAXPROCS(2)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
	t.Run("parallel control collides", func(t *testing.T) {
		status, collision, output := sharedIngressScenarios(t, featureConcurrency("core", 0))
		if status == 0 || !collision {
			t.Fatalf("parallel control did not collide on its actual listener: status=%d collision=%v\n%s", status, collision, output)
		}
	})
	t.Run("live lab retains exclusive ingress", func(t *testing.T) {
		status, collision, output := sharedIngressScenarios(t, featureConcurrency("vcs-lab", 0))
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

func TestUnitKubernetesLabScenariosRunAtMostOnePerNodeCPU(t *testing.T) {
	previous := runtime.GOMAXPROCS(10)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
	for _, c := range []struct {
		partition string
		cpus      int
		want      int
	}{
		{"kubernetes-lab", 2, 2},
		{"kubernetes-lab", 16, 10},
		{"kubernetes-lab", 0, 10},
		{"core", 2, 10},
		{"vcs-lab", 2, 1},
	} {
		if got := featureConcurrency(c.partition, c.cpus); got != c.want {
			t.Errorf("featureConcurrency(%q, %d) = %d, want %d", c.partition, c.cpus, got, c.want)
		}
	}
}

func TestUnitLabNodeCPUsRoundUpItsDockerLimit(t *testing.T) {
	for nano, want := range map[string]int{"2000000000\n": 2, "1500000000": 2, "0": 0} {
		if got, err := nodeCPUs(nano); err != nil || got != want {
			t.Errorf("nodeCPUs(%q) = %d, %v; want %d", nano, got, err, want)
		}
	}
	for _, nano := range []string{"", "two", "-1"} {
		if _, err := nodeCPUs(nano); !errors.Is(err, deps.ErrLab) {
			t.Errorf("nodeCPUs(%q) = %v, want ErrLab", nano, err)
		}
	}
}
