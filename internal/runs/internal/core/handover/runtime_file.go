package handover

import (
	"context"
	"errors"
	"maps"
	"slices"

	configapi "github.com/weaveplatform/infraweave/internal/configuration/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/runtimeconfig"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
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
		return runtimeconfig.File{}, false, err //nolint:wrapcheck // the VCS domain names the file read
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

// runtimeResolution keeps the first handover's merge beside the sealed values
// it records. Replay resolves only current stored sources and opens overrides.
type runtimeResolution struct {
	Custom      runtimeconfig.Input
	Effective   runtimeconfig.Input
	Repository  runtimeconfig.File
	HasFile     bool
	Environment configapi.Resolved
	Overrides   []lifecycle.Override
	Variables   []runtimeconfig.Variable
}

func (s *Service) resolveRuntime(ctx context.Context, r lifecycle.Run, st *stacksapi.Settings, labels []string) (runtimeResolution, error) {
	rc, err := runtimeconfig.Read(r.RuntimeConfig)
	if err != nil {
		return runtimeResolution{}, err //nolint:wrapcheck // the typed configuration names the read
	}
	custom := rc
	var repository runtimeconfig.File
	var hasFile bool
	var resolved configapi.Resolved
	if r.ResolvedConfig == nil {
		repository, hasFile, err = s.repositoryConfig(ctx, r, labels, st.Slug, st.Vendor)
		if err != nil {
			return runtimeResolution{}, err
		}
		if hasFile {
			rc = runtimeconfig.Overlay(repository.Settings(*st), custom)
		}
		if repository.Refusal != nil {
			rc = runtimeconfig.Input{}
		}
		if err := rc.ApplySettings(st); err != nil {
			return runtimeResolution{}, err //nolint:wrapcheck // the configuration names the refused setting
		}
		resolved, err = s.env.ResolveWithHooks(ctx, r.Account, r.Stack, rc.Hooks)
	} else {
		resolved, err = s.env.Resolve(ctx, r.Account, r.Stack)
	}
	if err != nil {
		return runtimeResolution{}, err //nolint:wrapcheck // the configuration domain names it
	}
	ovs, err := s.store.Overrides(ctx, r)
	if err != nil {
		return runtimeResolution{}, err //nolint:wrapcheck // the store names the override read
	}
	var fileVariables []runtimeconfig.Variable
	if r.ResolvedConfig == nil && hasFile && repository.Refusal == nil {
		fileVariables = repository.Variables(resolved.Elements)
		if len(fileVariables) > 0 {
			joined := make([]lifecycle.Override, 0, len(fileVariables)+len(ovs))
			for _, v := range fileVariables {
				joined = append(joined, lifecycle.Override{Name: v.Key, Value: v.Value})
			}
			ovs = append(joined, ovs...)
			if err := s.store.SetOverrides(ctx, r, ovs); err != nil {
				return runtimeResolution{}, err //nolint:wrapcheck // the store names the sealed write
			}
		}
	}
	return runtimeResolution{
		Custom: custom, Effective: rc, Repository: repository, HasFile: hasFile,
		Environment: resolved, Overrides: ovs, Variables: fileVariables,
	}, nil
}
