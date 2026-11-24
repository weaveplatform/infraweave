package http

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/core"
)

// awsReader answers rows for every read, recording each read's principal
// and stacks, or fails with err.
type awsReader struct {
	rows  map[kernel.ULID][]stacksapi.AwsAttachment
	err   error
	calls []awsRead
}

type awsRead struct {
	p      kernel.Principal
	stacks []kernel.ULID
}

func (r *awsReader) AwsAttachments(_ context.Context, p kernel.Principal, stacks []kernel.ULID) (map[kernel.ULID][]stacksapi.AwsAttachment, error) {
	r.calls = append(r.calls, awsRead{p: p, stacks: stacks})
	return r.rows, r.err
}

// TestUnitAwsAttachmentViews checks that a page of stacks reads its AWS
// integration attachments once, with the caller's principal, that each
// stack answers its own in the API's shape, a stack with none an empty
// list, and that a failed read fails the view.
func TestUnitAwsAttachmentViews(t *testing.T) {
	t.Parallel()
	server := New(core.New(core.Deps{Store: requestViewStore{}}), requestViewAuth{}, requestViewSpaces{}, nil)
	handed := server
	account := kernel.NewAccountID()
	p := kernel.Principal{AccountID: account, Login: "reader"}
	stacks := make([]core.Stack, 50)
	ids := make([]kernel.ULID, len(stacks))
	for n := range stacks {
		ids[n] = kernel.NewULID()
		stacks[n] = core.Stack{Settings: stacksapi.Settings{
			Stack:        stacksapi.Stack{ID: ids[n], Slug: fmt.Sprintf("s%02d", n), Space: account.ULID},
			VendorConfig: []byte(`{}`),
		}}
	}
	views, err := handed.views(t.Context(), p, stacks...)
	if err != nil || views[0].AttachedAwsIntegrations == nil || len(views[0].AttachedAwsIntegrations) != 0 {
		t.Fatalf("an unbound reader's attachments = %v, %v", views, err)
	}

	deploy, reader := kernel.NewULID(), kernel.NewULID()
	r := &awsReader{rows: map[kernel.ULID][]stacksapi.AwsAttachment{ids[0]: {
		{
			ID: "01JDIRECT", Integration: deploy, Name: "Deploy", RoleARN: "arn:aws:iam::222222222222:role/deploy",
			ExternalID: "acme@" + deploy.String() + "@s00@read", Duration: 3600, Read: true,
		},
		{
			ID: "auto-x", Integration: reader, Name: "Reader", RoleARN: "arn:aws:iam::222222222222:role/reader",
			ExternalID: "ours", Duration: 900, Labels: []string{"autoattach_write:prod"}, Worker: true,
			Write: true, Autoattached: true,
		},
	}}}
	server.BindAwsAttachments(r)
	views, err = handed.views(t.Context(), p, stacks...)
	if err != nil || len(views) != len(stacks) {
		t.Fatal(views, err)
	}
	if len(r.calls) != 1 || !reflect.DeepEqual(r.calls[0].p, p) || !slices.Equal(r.calls[0].stacks, ids) {
		t.Fatalf("reads = %+v, want one for the whole page", r.calls)
	}
	want := []api.StackAwsIntegrationAttachment{
		{
			Id: "01JDIRECT", IntegrationId: deploy.String(), Name: "Deploy",
			RoleArn: "arn:aws:iam::222222222222:role/deploy", ExternalId: "acme@" + deploy.String() + "@s00@read",
			DurationSeconds: 3600, Labels: []string{}, Read: true,
		},
		{
			Id: "auto-x", IntegrationId: reader.String(), Name: "Reader",
			RoleArn: "arn:aws:iam::222222222222:role/reader", ExternalId: "ours", DurationSeconds: 900,
			Labels: []string{"autoattach_write:prod"}, GenerateCredentialsInWorker: true, Write: true,
			IsAutoattached: true,
		},
	}
	got := views[0].AttachedAwsIntegrations
	if len(got) != len(want) {
		t.Fatalf("s00's attachments = %+v", got)
	}
	for n := range want {
		if !reflect.DeepEqual(got[n], want[n]) {
			t.Errorf("attachment %d = %+v, want %+v", n, got[n], want[n])
		}
	}
	if v := views[1].AttachedAwsIntegrations; v == nil || len(v) != 0 {
		t.Errorf("s01's attachments = %v, want an empty list", v)
	}

	broken := errors.New("cloud unavailable")
	server.BindAwsAttachments(&awsReader{err: broken})
	if _, err := handed.views(t.Context(), p, stacks...); !errors.Is(err, broken) {
		t.Fatalf("err = %v", err)
	}
}
