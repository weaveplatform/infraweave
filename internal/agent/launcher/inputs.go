package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/weaveplatform/infraweave/internal/agent/config"
	"github.com/weaveplatform/infraweave/internal/agent/executor"
	"github.com/weaveplatform/infraweave/internal/agent/phaseinputs"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// Inputs resolves the phase-scoped inputs of each assignment, afresh for
// each, so a changed value or file reaches the next run.
type Inputs interface {
	Resolve(ctx context.Context) (Resolved, error)
}

// Resolved is what one assignment receives beside its specification: the
// passthrough variables and scoped files its handoff carries, and the
// host paths or cluster objects every phase sees mounted read-only.
type Resolved struct {
	Inputs  phaseinputs.Inputs
	Mounts  []executor.Mount
	Volumes []executor.Volume
}

// Check checks r as one assignment receives it: its inputs and every
// target together.
func (r Resolved) Check() error {
	if err := phaseinputs.Check(r.Inputs); err != nil {
		return err //nolint:wrapcheck // phaseinputs names the input
	}
	mounted := make([]string, 0, len(r.Mounts)+len(r.Volumes))
	for _, m := range r.Mounts {
		mounted = append(mounted, m.Target)
	}
	for _, v := range r.Volumes {
		mounted = append(mounted, v.Target)
	}
	scoped := make([]string, 0, len(r.Inputs.Files))
	for _, f := range r.Inputs.Files {
		scoped = append(scoped, f.Target)
	}
	return phaseinputs.CheckTargets(mounted, scoped) //nolint:wrapcheck // as above
}

// Host is a launcher's own Inputs: the variables of Environ that
// launcher.whitelist_envs names, sensitive when launcher.mask_envs names
// them or the name the run sees, launcher.docker.extra_mounts as
// read-only host mounts, and the host files launcher.docker's
// ro_extra_mounts and wo_extra_mounts name, read whole for each
// assignment and sensitive, since a file's content is often a credential.
type Host struct {
	Config  config.Launcher
	Environ func(name string) (string, bool)
}

// errHostFile marks a scoped host path that is not a regular file of at
// most phaseinputs.MaxFile bytes.
var errHostFile = errors.New("a read or write extra mount must be a regular file of at most 1 MiB")

// Resolve reads h's variables and scoped files.
func (h Host) Resolve(context.Context) (Resolved, error) {
	env := h.Environ
	if env == nil {
		env = os.LookupEnv
	}
	var r Resolved
	for _, configured := range h.Config.WhitelistEnvs {
		v, ok := env(configured)
		if !ok {
			continue
		}
		name, e := phaseinputs.Name(configured)
		r.Inputs.Variables = append(r.Inputs.Variables, phaseinputs.Variable{
			Name: name, Exposure: e, Value: v,
			Sensitive: slices.Contains(h.Config.MaskEnvs, configured) || slices.Contains(h.Config.MaskEnvs, name),
		})
	}
	all, read, write := h.Config.Docker.Mounts()
	for _, m := range all {
		r.Mounts = append(r.Mounts, executor.Mount{Source: m.Source, Target: m.Target, ReadOnly: true})
	}
	for _, side := range []struct {
		e      phaseinputs.Exposure
		mounts []config.Mount
	}{{phaseinputs.Read, read}, {phaseinputs.Write, write}} {
		for _, m := range side.mounts {
			content, err := hostFile(m.Source)
			if err != nil {
				return Resolved{}, err
			}
			r.Inputs.Files = append(r.Inputs.Files, phaseinputs.File{
				Target: m.Target, Exposure: side.e, Content: content, Sensitive: true,
			})
		}
	}
	return r, nil
}

// hostFile reads the regular file at path, refusing one over
// phaseinputs.MaxFile bytes.
func hostFile(path string) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // a path the launcher's own configuration names
	if err != nil {
		return nil, fmt.Errorf("the extra mount %s: %w", path, err)
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("the extra mount %s: %w", path, errHostFile)
	}
	b, err := io.ReadAll(io.LimitReader(f, phaseinputs.MaxFile+1))
	if err != nil {
		return nil, fmt.Errorf("the extra mount %s: %w", path, err)
	}
	if len(b) > phaseinputs.MaxFile {
		return nil, fmt.Errorf("the extra mount %s: %w", path, errHostFile)
	}
	return b, nil
}

// executedVendors are the stack vendors whose runs the runner executes,
// the Terraform family's: iac registers no other.
var executedVendors = []string{"terraform", "opentofu", "terragrunt"}

// execution names what spec's container executes: the stack's vendor and
// the tool its runner calls, executor.Unknown for a name outside those
// the runner executes.
func execution(spec wire.RunSpec) executor.Execution {
	e := executor.Execution{Vendor: executor.Unknown, Tool: executor.Unknown}
	if slices.Contains(executedVendors, spec.Stack.Vendor) {
		e.Vendor = spec.Stack.Vendor
	}
	if spec.Tool != nil && spec.Tool.Name.Valid() {
		e.Tool = string(spec.Tool.Name)
	}
	return e
}

// toolSource names where spec's tool comes from: the verified release the
// launcher mounts, the image's own binary, or none.
func toolSource(spec wire.RunSpec) string {
	switch {
	case spec.Tool == nil:
		return "none"
	case spec.Tool.Download:
		return "release"
	}
	return "image"
}

// runtimeOf names e's runtime, executor.Unknown when it names none.
func runtimeOf(e executor.Executor) string {
	if n, ok := e.(executor.Named); ok {
		return n.Runtime()
	}
	return executor.Unknown
}
