package acceptance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

var statefulCaptureReplayers = map[string]func(*testing.T, *sdk.Client){
	"policy-timestamps":            replayPolicyTimestamps,
	"policy-create-engine-default": replayPolicyCreateEngineDefault,
	"policy-forced-deletion":       replayPolicyForcedDeletion,
}

func TestUnitStatefulCaptureReplayers(t *testing.T) {
	all, err := harness.LoadCaptures(capturesRoot, captureEntries(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, capture := range all {
		if capture.ReplayWith != "" && statefulCaptureReplayers[capture.ReplayWith] == nil {
			t.Errorf("%s has no REST replayer %q", capture.ID, capture.ReplayWith)
		}
	}
}

type timestampPolicy struct {
	ID        string `json:"id"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

func cleanupCapturePolicy(t *testing.T, client *sdk.Client, path string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Second)
		defer cancel()
		resp, err := client.Do(ctx, "DELETE", path, nil)
		if err != nil || resp == nil || resp.Status != 200 {
			t.Errorf("capture policy cleanup: %+v %v", resp, err)
			return
		}
		resp, err = client.Do(ctx, "GET", path, nil)
		if resp == nil || resp.Status != 404 {
			t.Errorf("capture cleanup did not remove policy: %+v %v", resp, err)
		}
	})
}

// C49 records upstream V0. The maintainer explicitly chose V1 for infraweave;
// this REST replay checks that approved deviation while retaining the raw fact.
func replayPolicyCreateEngineDefault(t *testing.T, client *sdk.Client) {
	t.Helper()
	resp, err := client.Do(t.Context(), "POST", "/api/v1/policies", map[string]any{
		"name": "C49 engine default", "type": "PLAN", "body": "package infraweave\n",
	})
	if err != nil || resp == nil || resp.Status != 201 {
		t.Fatalf("C49 create: %+v %v", resp, err)
	}
	var created struct {
		ID     string `json:"id"`
		Engine string `json:"engineType"`
	}
	if err := json.Unmarshal(resp.Body, &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Fatal("C49 create lacks policy identity")
	}
	path := "/api/v1/policies/" + created.ID
	cleanupCapturePolicy(t, client, path)
	if created.Engine != "REGO_V1" {
		t.Fatalf("C49 approved default changed: %s", resp.Body)
	}
	resp, err = client.Do(t.Context(), "GET", path, nil)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("C49 read: %+v %v", resp, err)
	}
	var stored struct {
		ID     string `json:"id"`
		Engine string `json:"engineType"`
	}
	if err := json.Unmarshal(resp.Body, &stored); err != nil {
		t.Fatal(err)
	}
	if stored != created {
		t.Fatalf("C49 default was not persisted: %+v versus %+v", created, stored)
	}
}

// C56's observed invariant is Unix seconds within each real request's bounds,
// with creation preserved on update. The local request uses infraweave's module
// namespace and owning-account root; no reference response is fabricated.
func replayPolicyTimestamps(t *testing.T, client *sdk.Client) {
	t.Helper()
	call := func(method, path string, body any, status int) (timestampPolicy, int64, int64) {
		t.Helper()
		before := time.Now().Unix()
		resp, err := client.Do(t.Context(), method, path, body)
		after := time.Now().Unix()
		if err != nil || resp == nil || resp.Status != status {
			t.Fatalf("C56 %s %s: %+v %v", method, path, resp, err)
		}
		var p timestampPolicy
		if err := json.Unmarshal(resp.Body, &p); err != nil {
			t.Fatal(err)
		}
		return p, before, after
	}
	created, before, after := call(
		"POST",
		"/api/v1/policies",
		map[string]any{"name": "C56 timestamps", "type": "PLAN", "engineType": "REGO_V1", "body": "package infraweave\nimport rego.v1\ndefault allow := true\n"},
		201,
	)
	if created.ID == "" {
		t.Fatal("C56 create lacks policy identity")
	}
	path := "/api/v1/policies/" + created.ID
	cleanupCapturePolicy(t, client, path)
	if created.CreatedAt < before || created.CreatedAt > after || created.UpdatedAt != created.CreatedAt {
		t.Fatalf("C56 create timestamps outside request bounds [%d,%d]: %+v", before, after, created)
	}
	timer := time.NewTimer(1100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-t.Context().Done():
		t.Fatal(context.Cause(t.Context()))
	case <-timer.C:
	}
	updated, before, after := call(
		"PUT",
		path,
		map[string]any{"name": "C56 timestamps", "body": "package infraweave\nimport rego.v1\ndefault allow := true\n# Timestamp update capture.\n"},
		200,
	)
	if updated.ID != created.ID || updated.CreatedAt != created.CreatedAt || updated.UpdatedAt < before || updated.UpdatedAt > after || updated.UpdatedAt <= created.UpdatedAt {
		t.Fatalf("C56 update timestamps outside request bounds [%d,%d] or changed creation: before=%+v after=%+v", before, after, created, updated)
	}
	read, _, _ := call("GET", path, nil, 200)
	if read != updated {
		t.Fatalf("C56 persisted timestamps differ from update: %+v versus %+v", read, updated)
	}
}
