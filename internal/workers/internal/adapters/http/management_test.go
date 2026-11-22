package http_test

import (
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

func TestUnitManagedPoolLookupAndProjection(t *testing.T) {
	f := newFixture(t)
	owner := kernel.WorkerOwner{ClusterID: "cluster", Namespace: "workers", ResourceUID: "opaque-kubernetes-uid"}
	row := f.store.rows[f.ids[0]]
	row.Management = &owner
	row.Version = 7
	f.store.rows[row.ID] = row
	req := api.WorkerPoolManagedRequestObject{Params: api.WorkerPoolManagedParams{ClusterID: owner.ClusterID, Namespace: owner.Namespace, ResourceUID: owner.ResourceUID}}
	if _, err := f.srv.WorkerPoolManaged(t.Context(), req); err == nil {
		t.Fatal("anonymous owner lookup accepted")
	}
	ctx := signedIn(t)
	got, err := f.srv.WorkerPoolManaged(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	v := got.(api.WorkerPoolManaged200JSONResponse)
	if v.Body.Id != row.ID.String() || !v.Body.ManagedByK8sController || v.Body.Management == nil || v.Body.Management.ResourceUID != owner.ResourceUID || v.Headers.ETag == nil ||
		*v.Headers.ETag != "\"7\"" {
		t.Fatalf("managed projection: %+v", v)
	}
	ordinary, err := f.srv.WorkerPool(ctx, api.WorkerPoolRequestObject{WorkerPoolId: f.ids[1].String()})
	if err != nil {
		t.Fatal(err)
	}
	if v := ordinary.(api.WorkerPool200JSONResponse).Body; v.ManagedByK8sController || v.Management != nil {
		t.Fatalf("unmanaged pool claimed ownership: %+v", v)
	}
	req.Params.ResourceUID = "unknown"
	if _, err := f.srv.WorkerPoolManaged(ctx, req); err == nil {
		t.Fatal("unknown UID recovered a pool")
	} else if p, ok := errors.AsType[*problem.Error](err); !ok || p.Problem.Status != 404 {
		t.Fatalf("unknown owner problem: %v", err)
	}
	req.Params.ResourceUID = owner.ResourceUID
	f.az.fail = errBoom
	if _, err := f.srv.WorkerPoolManaged(ctx, req); !errors.Is(err, errBoom) {
		t.Fatalf("authorization outage hidden: %v", err)
	}
	f.az.fail = nil
	f.store.fail = errBoom
	if _, err := f.srv.WorkerPoolManaged(ctx, req); !errors.Is(err, errBoom) {
		t.Fatalf("storage outage hidden: %v", err)
	}
}

func TestUnitManagedRequestsCannotInferIdentityFromReadView(t *testing.T) {
	f := newFixture(t)
	ctx := signedIn(t)
	owner := kernel.WorkerOwner{ClusterID: "cluster", Namespace: "workers", ResourceUID: "opaque-kubernetes-uid"}
	row := f.store.rows[f.ids[0]]
	row.Management = &owner
	f.store.rows[row.ID] = row
	patch := api.WorkerPoolPatchApplicationMergePatchPlusJSONRequestBody{"description": "unrelated client"}
	if _, err := f.srv.WorkerPoolPatch(ctx, api.WorkerPoolPatchRequestObject{WorkerPoolId: row.ID.String(), Body: &patch}); err == nil {
		t.Fatal("merge patch inferred management authority")
	} else if p, ok := errors.AsType[*problem.Error](err); !ok || p.Problem.Status != 409 {
		t.Fatalf("missing owner patch problem: %v", err)
	}
	_, err := f.srv.WorkerPoolCreate(
		ctx,
		api.WorkerPoolCreateRequestObject{
			Body: &api.WorkerPoolCreateJSONRequestBody{
				Name:                      "managed",
				CertificateSigningRequest: f.csr,
				Management:                &api.WorkerPoolManagement{ClusterID: owner.ClusterID, Namespace: owner.Namespace, ResourceUID: owner.ResourceUID},
			},
		},
	)
	if p, ok := errors.AsType[*problem.Error](err); !ok || p.Problem.Status != 400 {
		t.Fatalf("missing idempotency key reached creation: %v", err)
	}
}
