package core

import (
	"context"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

type runtimeStore struct {
	Store
	attachmentsErr, contextsErr, elementsErr error
}

func (s runtimeStore) Attachments(context.Context, kernel.AccountID, []kernel.ULID, bool) (map[kernel.ULID][]Attachment, error) {
	return nil, s.attachmentsErr
}

func (s runtimeStore) ByIDs(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID]Context, error) {
	return nil, s.contextsErr
}

func (s runtimeStore) Elements(context.Context, kernel.AccountID, []Owner) (map[kernel.ULID][]Element, error) {
	return nil, s.elementsErr
}

// TestUnitRuntimeConfigReads checks that empty input needs no store and
// that any failed batch read fails the view instead of showing defaults.
func TestUnitRuntimeConfigReads(t *testing.T) {
	if got, err := New(Deps{}).RuntimeConfig(t.Context(), kernel.NewAccountID(), nil); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty input = %v, %v", got, err)
	}
	down := errors.New("store unavailable")
	for name, store := range map[string]runtimeStore{
		"attachments": {attachmentsErr: down},
		"contexts":    {contextsErr: down},
		"elements":    {elementsErr: down},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := New(Deps{Store: store}).RuntimeConfig(t.Context(), kernel.NewAccountID(), []kernel.ULID{kernel.NewULID()})
			if !errors.Is(err, down) || got != nil {
				t.Errorf("failed read = %v, %v", got, err)
			}
		})
	}
}
