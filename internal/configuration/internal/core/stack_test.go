package core

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

type nameStore struct {
	Store
	elementErr, attachmentsErr, elementsErr error
	attached                                []Attachment
	elements                                map[kernel.ULID][]Element
}

func (s nameStore) Element(context.Context, kernel.AccountID, Owner, string) (Element, error) {
	return Element{}, s.elementErr
}

func (s nameStore) Attachments(_ context.Context, _ kernel.AccountID, ids []kernel.ULID, byStack bool) (map[kernel.ULID][]Attachment, error) {
	if !byStack || len(ids) != 1 {
		panic("expected one stack's attachments")
	}
	return map[kernel.ULID][]Attachment{ids[0]: s.attached}, s.attachmentsErr
}

func (s nameStore) Elements(context.Context, kernel.AccountID, []Owner) (map[kernel.ULID][]Element, error) {
	return s.elements, s.elementsErr
}

// TestUnitStackElementName checks that a failed read cannot admit a new
// name, and that replacing a stack's own name does not read its contexts.
func TestUnitStackElementName(t *testing.T) {
	down := errors.New("store unavailable")
	stack, contextID := kernel.NewULID(), kernel.NewULID()
	attached := []Attachment{{Context: contextID, Stack: stack}}
	for _, tt := range []struct {
		name  string
		store nameStore
		want  error
	}{
		{
			name:  "replacement",
			store: nameStore{attachmentsErr: down},
		},
		{
			name:  "element read fails",
			store: nameStore{elementErr: down}, want: down,
		},
		{
			name:  "attachment read fails",
			store: nameStore{elementErr: ErrNotFound, attachmentsErr: down}, want: down,
		},
		{
			name:  "unattached",
			store: nameStore{elementErr: ErrNotFound, elementsErr: down},
		},
		{
			name:  "context element read fails",
			store: nameStore{elementErr: ErrNotFound, attached: attached, elementsErr: down}, want: down,
		},
		{
			name: "unused name",
			store: nameStore{
				elementErr: ErrNotFound, attached: attached,
				elements: map[kernel.ULID][]Element{contextID: {{Name: "OTHER"}}},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := New(Deps{Store: tt.store})
			if err := s.stackElementName(t.Context(), kernel.NewAccountID(), Owner{Stack: stack}, "REGION", "/input/id"); !errors.Is(err, tt.want) {
				t.Errorf("name check = %v, want %v", err, tt.want)
			}
		})
	}
	s := New(Deps{Store: nameStore{
		elementErr: ErrNotFound, attached: attached,
		elements: map[kernel.ULID][]Element{contextID: {{Name: "REGION", WriteOnly: true}}},
	}})
	err := s.stackElementName(t.Context(), kernel.NewAccountID(), Owner{Stack: stack}, "REGION", "/input/id")
	inv, ok := errors.AsType[*kernel.Invalid](err)
	if !ok || inv.Detail != "The config element is refused." || !reflect.DeepEqual(inv.Errors, []kernel.FieldError{{
		Pointer: "/input/id", Reason: kernel.ReasonDuplicate, Message: "an attached context already sets this name",
	}}) {
		t.Errorf("a taken name = %v", err)
	}
}
