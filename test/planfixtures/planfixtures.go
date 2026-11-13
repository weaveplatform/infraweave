package planfixtures

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"

	"github.com/weaveplatform/infraweave/test/deps"
)

// Scenario is one recorded plan: Before's files are applied, then
// After's are planned, so the plan holds the changes from one to the
// other.
type Scenario struct {
	Name          string
	Before, After map[string]string
}

// before is the configuration every scenario applies first: a resource of
// each kind the full scenario changes, and an output. terraform_data is
// the tools' own resource, so no provider is downloaded.
const before = `resource "terraform_data" "change" {
  input = "one"
}

resource "terraform_data" "delete" {
  input = "one"
}

resource "terraform_data" "replace" {
  triggers_replace = "one"
}

resource "terraform_data" "replace_first" {
  triggers_replace = "one"

  lifecycle {
    create_before_destroy = true
  }
}

resource "terraform_data" "keep" {
  input = "one"
}

resource "terraform_data" "old" {
  input = "one"
}

resource "terraform_data" "forget" {
  input = "one"
}

output "value" {
  value = "one"
}
`

// after plans one change of every plan.Type but CREATE_AFTER_FORGOTTEN,
// which neither tool's configuration language plans: an addition, an
// in-place change, a deletion, both replacements, a resource kept, one
// moved, one forgotten, one imported, a data source read at apply, and a
// changed output.
const after = `resource "terraform_data" "add" {
  input = "one"
}

resource "terraform_data" "change" {
  input = "two"
}

resource "terraform_data" "replace" {
  triggers_replace = "two"
}

resource "terraform_data" "replace_first" {
  triggers_replace = "two"

  lifecycle {
    create_before_destroy = true
  }
}

resource "terraform_data" "keep" {
  input = "one"
}

moved {
  from = terraform_data.old
  to   = terraform_data.new
}

resource "terraform_data" "new" {
  input = "one"
}

removed {
  from = terraform_data.forget

  lifecycle {
    destroy = false
  }
}

import {
  to = terraform_data.imported
  id = "imported"
}

resource "terraform_data" "imported" {}

data "terraform_remote_state" "read" {
  backend = "local"

  config = {
    path = "${terraform_data.add.id}.tfstate"
  }
}

output "value" {
  value = "two"
}
`

// Scenarios are the plans each tool records: every change type at once,
// no change at all, and a change to an output alone.
var Scenarios = []Scenario{
	{
		Name:   "all",
		Before: map[string]string{"main.tf": before},
		After:  map[string]string{"main.tf": after},
	},
	{
		Name:   "none",
		Before: map[string]string{"main.tf": before},
		After:  map[string]string{"main.tf": before},
	},
	{
		Name:   "outputs",
		Before: map[string]string{"main.tf": before},
		After: map[string]string{
			"main.tf": strings.Replace(before, `value = "one"`, `value = "two"`, 1),
		},
	},
}

// Tools are the pinned releases whose plans are recorded.
var Tools = []deps.Tool{deps.Terraform, deps.OpenTofu}

// image is the image each tool's container starts from: a filesystem with
// a temporary directory and the workspace, and nothing else, so nothing
// is pulled. The tool is mounted into it.
const image = "infraweave-planfixtures:local"

// The paths the tool and the workspace are mounted at.
const (
	toolPath      = "/opt/tool"
	workspacePath = "/mnt/workspace"
)

// ErrTool is a tool's command that exits other than 0.
var ErrTool = errors.New("the tool failed")

// ErrArch is a Docker daemon of an architecture no tool pins.
var ErrArch = errors.New("no GOARCH for the Docker daemon's architecture")

// Record records each of scenarios for each of tools into
// dir/<tool>/<scenario>.json, indented, with a README naming the
// releases, running each tool's pinned Linux binary in a container with
// no network. d fetches and checks the binaries.
func Record(
	ctx context.Context,
	d *deps.Dependencies,
	tools []deps.Tool,
	scenarios []Scenario,
	dir string,
) error {
	cli, err := testcontainers.NewDockerClientWithOpts(ctx)
	if err != nil {
		return fmt.Errorf("docker: %w", err)
	}
	defer cli.Close()
	info, err := cli.Info(ctx, client.InfoOptions{})
	if err != nil {
		return fmt.Errorf("docker: %w", err)
	}
	arch := map[string]string{"x86_64": "amd64", "aarch64": "arm64"}[info.Info.Architecture]
	if arch == "" {
		return fmt.Errorf("%w: %q", ErrArch, info.Info.Architecture)
	}
	if err := importImage(ctx, cli); err != nil {
		return err
	}
	for _, t := range tools {
		bin, err := d.Tool(ctx, t, arch)
		if err != nil {
			return err //nolint:wrapcheck // names the tool and the release
		}
		out := filepath.Join(dir, t.Name)
		if err := os.MkdirAll(out, 0o750); err != nil {
			return fmt.Errorf("fixtures: %w", err)
		}
		for _, s := range scenarios {
			doc, err := record(ctx, cli, bin, s)
			if err != nil {
				return fmt.Errorf("%s %s, %s: %w", t.Name, t.Version, s.Name, err)
			}
			if err := os.WriteFile(filepath.Join(out, s.Name+".json"), doc, 0o600); err != nil {
				return fmt.Errorf("fixtures: %w", err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), readme(tools, scenarios),
		0o600); err != nil {
		return fmt.Errorf("fixtures: %w", err)
	}
	return nil
}

// record applies s.Before and plans s.After with bin in a fresh
// workspace, and answers the plan's JSON, indented.
func record(
	ctx context.Context,
	cli *testcontainers.DockerClient,
	bin string,
	s Scenario,
) ([]byte, error) {
	// Under the system's temporary directory, which Docker Desktop shares
	// with its virtual machine, so the container mounts it.
	ws, err := os.MkdirTemp("", "infraweave-plan-")
	if err != nil {
		return nil, fmt.Errorf("workspace: %w", err)
	}
	defer os.RemoveAll(ws)
	if err := write(ws, s.Before); err != nil {
		return nil, err
	}
	for _, args := range [][]string{
		{"init", "-input=false"},
		{"apply", "-input=false", "-auto-approve"},
	} {
		if _, err := run(ctx, cli, bin, ws, args); err != nil {
			return nil, err
		}
	}
	if err := write(ws, s.After); err != nil {
		return nil, err
	}
	if _, err := run(
		ctx,
		cli,
		bin,
		ws,
		[]string{"plan", "-input=false", "-out=plan.tfplan"},
	); err != nil {
		return nil, err
	}
	out, err := run(ctx, cli, bin, ws, []string{"show", "-json", "plan.tfplan"})
	if err != nil {
		return nil, err
	}
	var doc bytes.Buffer
	if err := json.Indent(&doc, out, "", "  "); err != nil {
		return nil, fmt.Errorf("the plan's JSON: %w", err)
	}
	doc.WriteByte('\n')
	return doc.Bytes(), nil
}

// write writes files into dir, removing the .tf files it held.
func write(dir string, files map[string]string) error {
	old, _ := filepath.Glob(filepath.Join(dir, "*.tf")) // a constant pattern
	for _, f := range old {
		if err := os.Remove(f); err != nil {
			return fmt.Errorf("workspace: %w", err)
		}
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			return fmt.Errorf("workspace: %w", err)
		}
	}
	return nil
}

// run runs bin with args in a container whose workspace is ws, as the
// host's user so it can write there, and answers its standard output.
func run(
	ctx context.Context,
	cli *testcontainers.DockerClient,
	bin, ws string,
	args []string,
) ([]byte, error) {
	c, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:      image,
			User:       strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()),
			Entrypoint: []string{toolPath},
			Cmd:        args,
			Env: []string{
				"HOME=/tmp", "TF_IN_AUTOMATION=1", "CHECKPOINT_DISABLE=1", "NO_COLOR=1",
			},
			WorkingDir: workspacePath,
		},
		HostConfig: &container.HostConfig{
			Binds:       []string{bin + ":" + toolPath + ":ro", ws + ":" + workspacePath},
			NetworkMode: "none",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("container: %w", err)
	}
	defer func() {
		_, _ = cli.ContainerRemove(context.WithoutCancel(ctx), c.ID, client.ContainerRemoveOptions{
			Force: true,
		}) // a container left behind is the daemon's to collect
	}()
	if _, err := cli.ContainerStart(ctx, c.ID, client.ContainerStartOptions{}); err != nil {
		return nil, fmt.Errorf("container: %w", err)
	}
	w := cli.ContainerWait(ctx, c.ID, client.ContainerWaitOptions{
		Condition: container.WaitConditionNotRunning,
	})
	var code int64
	select {
	case res := <-w.Result:
		code = res.StatusCode
	case err := <-w.Error:
		return nil, fmt.Errorf("container: %w", err)
	}
	logs, err := cli.ContainerLogs(ctx, c.ID, client.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true,
	})
	if err != nil {
		return nil, fmt.Errorf("container: %w", err)
	}
	defer logs.Close()
	var out, errOut bytes.Buffer
	if _, err := stdcopy.StdCopy(&out, &errOut, logs); err != nil {
		return nil, fmt.Errorf("container: %w", err)
	}
	if code != 0 {
		return nil, fmt.Errorf("%w: %s exited %d: %s%s", ErrTool, args[0], code, out.Bytes(),
			errOut.Bytes())
	}
	return out.Bytes(), nil
}

// importImage imports image, replacing any of the name.
func importImage(ctx context.Context, cli *testcontainers.DockerClient) error {
	var root bytes.Buffer
	tw := tar.NewWriter(&root)
	for _, h := range []tar.Header{
		{Name: "mnt/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "mnt/workspace/", Typeflag: tar.TypeDir, Mode: 0o777},
		{Name: "opt/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "tmp/", Typeflag: tar.TypeDir, Mode: 0o1777},
	} {
		if err := tw.WriteHeader(&h); err != nil {
			return fmt.Errorf("image: %w", err)
		}
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("image: %w", err)
	}
	res, err := cli.ImageImport(ctx, client.ImageImportSource{Source: &root, SourceName: "-"},
		image, client.ImageImportOptions{})
	if err != nil {
		return fmt.Errorf("image: %w", err)
	}
	defer res.Close()
	if _, err := io.Copy(io.Discard, res); err != nil {
		return fmt.Errorf("image: %w", err)
	}
	return nil
}

// readme is the fixtures' README: what they are, the releases that wrote
// them, and how to record them again.
func readme(tools []deps.Tool, scenarios []Scenario) []byte {
	var b strings.Builder
	b.WriteString("# Plan fixtures\n\n")
	b.WriteString("`show -json` of a saved plan, written by `make iac-fixtures` " +
		"(test/planfixtures) with the releases test/deps pins:\n\n")
	for _, t := range tools {
		fmt.Fprintf(&b, "- `%s/`: %s %s\n", t.Name, t.Name, t.Version)
	}
	b.WriteString("\nEach applies one configuration and plans another:\n\n")
	for _, s := range scenarios {
		fmt.Fprintf(&b, "- `%s.json`: %s\n", s.Name, describe[s.Name])
	}
	b.WriteString("\nRecord them again after a release moves, and update the tests " +
		"that read them if a release plans otherwise.\n")
	return []byte(b.String())
}

// describe says what each scenario plans.
var describe = map[string]string{
	"all":     "one change of every type, a data source read at apply, and a changed output",
	"none":    "no change",
	"outputs": "a change to an output alone",
}
