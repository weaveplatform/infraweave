package push_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/push"
)

type capReader struct {
	push.LimitStore
	account kernel.AccountID
	value   *int
	err     error
}

func (s *capReader) RunLimit(_ context.Context, account kernel.AccountID) (*int, error) {
	s.account = account
	return s.value, s.err
}

func TestUnitEffectiveLimit(t *testing.T) {
	account := kernel.NewAccountID()
	store := &capReader{}
	limits := push.Limits{Store: store, Ceiling: 500}
	for _, tc := range []struct {
		value *int
		want  int
	}{{nil, 500}, {new(2), 2}, {new(700), 500}} {
		store.value = tc.value
		got, err := limits.Effective(t.Context(), account)
		if err != nil || got != tc.want || store.account != account {
			t.Fatal("effective cap lost account/default/ceiling", got, err)
		}
	}
	cause := errors.New("database unavailable")
	store.err = cause
	if _, err := limits.Effective(t.Context(), account); !errors.Is(err, cause) {
		t.Fatal("effective cap lost storage failure", err)
	}
}

func TestUnitLimitBoundsBeforeWrite(t *testing.T) {
	limits := push.Limits{Ceiling: 2}
	for _, value := range []int{-1, 0, 3} {
		_, err := limits.Set(t.Context(), kernel.Principal{}, &value, "")
		invalid, ok := errors.AsType[*kernel.Invalid](err)
		if !ok || len(invalid.Errors) != 1 || invalid.Errors[0].Pointer != "/limit" || !strings.Contains(invalid.Errors[0].Message, "ceiling of 2") {
			t.Fatal("invalid cap reached a write or hid its ceiling", err)
		}
	}
}
