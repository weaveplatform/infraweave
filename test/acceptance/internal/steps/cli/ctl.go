package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// ctlSteps run infraweavectl against the scenario's servers, with a
// profiles file of the scenario's own.
type ctlSteps struct {
	w *harness.World
}

func (c *ctlSteps) register(sc *godog.ScenarioContext) {
	sc.Step(
		`^infraweavectl's profile "(\w+)" holds ([AB])'s endpoint and the operator key$`,
		c.profile,
	)
	sc.Step(`^infraweavectl whoami prints the claims of an operator token$`, c.whoami)
	sc.Step(`^infraweavectl version names infraweavectl$`, c.version)
	sc.Step(`^infraweavectl "([^"]+)" prints JSON holding '([^']*)'$`, c.prints)
	sc.Step(`^infraweavectl "([^"]+)"( with the home key)? fails with a (\d+) problem "([a-z-]+)"$`,
		c.fails)
}

// run runs infraweavectl with args, stdin as standard input and env over
// an environment that sets none of its variables.
func (c *ctlSteps) run(
	ctx context.Context,
	stdin string,
	env []string,
	args ...string,
) (string, error) {
	bin, err := harness.CtlBinary(ctx)
	if err != nil {
		return "", err
	}
	w := c.w
	cmd := exec.CommandContext(ctx, bin,
		append([]string{"--config", filepath.Join(w.Dir, "infraweavectl.yaml")}, args...)...)
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "INFRAWEAVECTL_") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(append(cmd.Env, "GOCOVERDIR="+w.Suite.CoverDir), env...)
	cmd.Stdin = strings.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return string(out), fmt.Errorf("infraweavectl %s: %w: %s", strings.Join(args, " "), err,
			stderr.String())
	}
	return string(out), nil
}

func (c *ctlSteps) profile(ctx context.Context, name, replica string) error {
	i := 0
	if replica == "B" {
		i = 1
	}
	_, err := c.run(ctx, harness.OperatorSecret()+"\n", nil, "profile", "set", name,
		"--endpoint", "http://"+c.w.Servers[i].API, "--api-key", harness.OperatorKey)
	return err
}

func (c *ctlSteps) whoami(ctx context.Context) error {
	out, err := c.run(ctx, "", nil, "whoami")
	if err != nil {
		return err
	}
	var claims struct {
		Subject string `json:"sub"`
		Kind    string `json:"iw_kind"` //nolint:tagliatelle // the claim is named iw_kind on the wire
	}
	if err := json.Unmarshal([]byte(out), &claims); err != nil ||
		claims.Subject != "operator::"+harness.OperatorKey || claims.Kind != "operator" {
		return fmt.Errorf("%w: whoami printed %s", harness.ErrStep, out)
	}
	return nil
}

func (c *ctlSteps) version(ctx context.Context) error {
	out, err := c.run(ctx, "", nil, "version")
	if err == nil && !strings.HasPrefix(out, "infraweavectl ") {
		err = fmt.Errorf("%w: version printed %q", harness.ErrStep, out)
	}
	return err
}

// prints checks that the command printed a JSON object holding each of
// want's members, as want has them.
func (c *ctlSteps) prints(ctx context.Context, command, want string) error {
	out, err := c.run(ctx, "", nil, strings.Fields(command)...)
	if err != nil {
		return err
	}
	var got, exp map[string]any
	if err := json.Unmarshal([]byte(want), &exp); err != nil {
		return fmt.Errorf("%w: the step's JSON %q: %w", harness.ErrStep, want, err)
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		return fmt.Errorf("%w: infraweavectl %s printed %s", harness.ErrStep, command, out)
	}
	for k, v := range exp {
		if !reflect.DeepEqual(got[k], v) {
			return fmt.Errorf(
				"%w: infraweavectl %s printed %s, want %s",
				harness.ErrStep,
				command,
				out,
				want,
			)
		}
	}
	return nil
}

// fails checks that the command exits 1 having printed the problem.
func (c *ctlSteps) fails(ctx context.Context, command, homeKey, code, slug string) error {
	var env []string
	if homeKey != "" {
		env = []string{
			"INFRAWEAVECTL_API_KEY=" + harness.Home.Key,
			"INFRAWEAVECTL_API_KEY_SECRET=" + harness.Home.Secret(),
		}
	}
	out, runErr := c.run(ctx, "", env, strings.Fields(command)...)
	if exit, ok := errors.AsType[*exec.ExitError](runErr); !ok || exit.ExitCode() != 1 {
		return fmt.Errorf(
			"%w: infraweavectl %s exited with %w, want 1",
			harness.ErrStep,
			command,
			runErr,
		)
	}
	var p sdk.Problem
	if err := json.Unmarshal([]byte(out), &p); err != nil || fmt.Sprint(p.Status) != code ||
		p.Slug() != slug {
		return fmt.Errorf(
			"%w: infraweavectl %s printed %s, want a %s problem %q: %w",
			harness.ErrStep,
			command,
			out,
			code,
			slug,
			runErr,
		)
	}
	return nil
}
