package handover

import (
	"context"
	"errors"
	"maps"
	"slices"

	"github.com/weaveplatform/infraweave/internal/kernel"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/runtimeconfig"
)

func (s *Service) repositoryConfig(ctx context.Context, r lifecycle.Run, labels []string, slug, vendor string) (runtimeconfig.File, bool, error) {
	if r.LocalPreview() {
		return runtimeconfig.File{Notices: []string{"local preview uses an archive; repository runtime configuration is not read"}}, false, nil
	}
	if slices.Contains(labels, "feature:ignore_runtime_config") {
		return runtimeconfig.File{}, false, nil
	}
	if s.sources == nil {
		return runtimeconfig.File{}, false, errNoSources
	}
	data, found, err := s.sources.File(ctx, r.Account, r.Stack, r.Commit.Hash, runtimeconfig.FilePath)
	if errors.Is(err, runsapi.ErrFileTooLarge) {
		return runtimeconfig.File{Refusal: &runtimeconfig.FileError{Path: runtimeconfig.FilePath, Reason: kernel.ReasonRule, Message: "the file exceeds 1 MiB"}}, true, nil
	}
	if err != nil || !found {
		return runtimeconfig.File{}, false, err
	}
	return runtimeconfig.ParseFile(data, slug, vendor, s.unknownKeys), true, nil
}

// runtimeKeys contains configuration-variable names alone. Values are sealed
// separately; proposal overrides do not become a custom configuration read.
func runtimeKeys(file, custom []runtimeconfig.Variable) []runtimeconfig.Variable {
	names := map[string]bool{}
	for _, values := range [][]runtimeconfig.Variable{file, custom} {
		for _, v := range values {
			names[v.Key] = true
		}
	}
	out := make([]runtimeconfig.Variable, 0, len(names))
	for _, name := range slices.Sorted(maps.Keys(names)) {
		out = append(out, runtimeconfig.Variable{Key: name})
	}
	return out
}
