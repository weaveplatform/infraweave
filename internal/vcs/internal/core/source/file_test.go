package source

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type fileSource struct {
	port.Source
	data      []byte
	err       error
	ref, path string
}

func (s *fileSource) ReadFile(_ context.Context, _ port.Repository, ref, path string) ([]byte, error) {
	s.ref, s.path = ref, path
	return s.data, s.err
}

func TestUnitRunSourceFile(t *testing.T) {
	url := "https://git.example/acme/infra.git"
	for _, tc := range []struct {
		name      string
		data      []byte
		err, want error
		found     bool
	}{
		{"present", []byte("version: '2'"), nil, nil, true},
		{"empty", []byte{}, nil, nil, true},
		{"missing", nil, port.ErrNotFound, nil, false},
		{"provider unavailable", nil, errOffline, errOffline, false},
		{"provider cap", nil, port.ErrTooLarge, runsapi.ErrFileTooLarge, false},
		{"at limit", []byte(strings.Repeat("x", runsapi.SourceFileLimit)), nil, nil, true},
		{"over limit", []byte(strings.Repeat("x", runsapi.SourceFileLimit+1)), nil, runsapi.ErrFileTooLarge, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &fileSource{data: tc.data, err: tc.err}
			s := Runs{newService(stacks{settings: settings("GIT", &url)}, map[string]port.Source{"GIT": provider})}
			data, found, err := s.File(t.Context(), kernel.NewAccountID(), kernel.NewULID(), "immutable-sha", ".infraweave/config.yml")
			if !errors.Is(err, tc.want) || found != tc.found || tc.found && string(data) != string(tc.data) ||
				provider.ref != "immutable-sha" || provider.path != ".infraweave/config.yml" {
				t.Fatalf("File = %d bytes, %t, %v; request %s %s", len(data), found, err, provider.ref, provider.path)
			}
		})
	}
	s := Runs{newService(stacks{err: errOffline}, nil)}
	if _, _, err := s.File(t.Context(), kernel.NewAccountID(), kernel.NewULID(), "sha", "file"); !errors.Is(err, errOffline) {
		t.Fatalf("stack read failure: %v", err)
	}
}
