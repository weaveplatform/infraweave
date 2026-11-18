package http

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
)

func TestUnitDeliveryProjection(t *testing.T) {
	t.Parallel()
	d := intake.Delivery{State: intake.Dispatched, RunsCreated: math.MaxInt32 + 1, Body: []byte("private-marker"), Signature: []byte("private-marker"), BodyKey: "private-marker"}
	out := deliveryView(d)
	raw, err := json.Marshal(out)
	if err != nil || strings.Contains(string(raw), "private-marker") || out.State != "DISPATCHED" || out.RunsCreated != math.MaxInt32 {
		t.Fatal("public delivery leaked retained fields or invalid counter", string(raw), err)
	}
	for _, tc := range []struct {
		name   string
		in     error
		status int
	}{
		{"missing", intake.ErrNotFound, http.StatusNotFound},
		{"conflict", &intake.ReplayConflict{Detail: "The delivery still has pending or running work."}, http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := deliveryError(tc.in)
			p, ok := errors.AsType[*problem.Error](out)
			if !ok || p.Problem.Status != tc.status {
				t.Fatal("wrong delivery refusal", out)
			}
		})
	}
	cause := errors.New("unavailable")
	if !errors.Is(deliveryError(cause), cause) {
		t.Fatal("infrastructure error was rewritten")
	}
}
