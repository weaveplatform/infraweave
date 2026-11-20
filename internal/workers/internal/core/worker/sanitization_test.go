package worker_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/workers/internal/core/worker"
)

func TestUnitWorkerSanitizationCapability(t *testing.T) {
	for _, capabilities := range []any{nil, []string{}, []string{"unrelated"}, []string{strings.Repeat("x", 65)}, make([]string, 17)} {
		f := newFixture(t, worker.Deps{})
		payload := f.payload()
		payload["capabilities"] = capabilities
		_, err := f.svc.Register(t.Context(), jws(t, f.key, f.cert, payload))
		var invalid *kernel.Invalid
		if !errors.As(err, &invalid) || len(f.store.workers) != 0 {
			t.Fatalf("incapable worker registered: %v", err)
		}
	}
}
