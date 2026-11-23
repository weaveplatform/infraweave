package workers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/weaveplatform/infraweave/internal/agent/executor/docker"
	"github.com/weaveplatform/infraweave/internal/agent/executor/kubernetes"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// The fixture's phase-scoped inputs, as a scenario's feature describes
// them: the variable PHASE_TOKEN, read-t0ken to the read phases and
// write-t0ken to the write phases, PHASE_CACHE, /opt/cache to every
// phase, and the files /mnt/keys/read.txt, holding read-file, to the read
// phases, /mnt/keys/write.txt, holding write-f1le, to the write phases,
// and /mnt/shared/shared.txt, holding shared-file, read-only to every
// phase.
const (
	inputReadToken  = "read-t0ken"
	inputWriteToken = "write-t0ken"
	inputCache      = "/opt/cache"
	inputReadFile   = "read-file"
	inputWriteFile  = "write-f1le"
	inputShared     = "shared-file"
	inputKeysDir    = "/mnt/keys"
	inputSharedFile = "/mnt/shared/shared.txt"
)

// inputValues are the fixture's values a run's container or Pod must
// never carry: the tokens and the scoped files' contents.
var inputValues = []string{inputReadToken, inputWriteToken, inputReadFile, inputWriteFile}

// maskedValues are those no launcher or controller log line holds: the
// runner output it logs is masked, and only a Docker launcher's read token
// is not sensitive.
var maskedValues = []string{inputWriteToken, inputReadFile, inputWriteFile}

// outputPattern is a step's double-quoted text, with Go's escapes.
const outputPattern = `"((?:[^"\\]|\\.)*)"`

func (l *launcherSteps) registerPhaseInputs(sc *godog.ScenarioContext) {
	sc.Step(`^the launchers started from now on receive the phase-scoped inputs$`,
		func() { l.phaseInputs = true })
	sc.Step(`^the run "(\w+)"'s container executes the vendor "(\w+)" with the tool "(\w+)" `+
		`and carries no phase-scoped value$`, l.container)
	sc.Step(`^the launcher "(\w+)" logged the run "(\w+)"'s container on the runtime "(\w+)" `+
		`executing the vendor "(\w+)" with the tool "(\w+)" of the (release|image), and no phase-scoped value$`,
		func(name, run, runtime, vendor, tool, source string) error {
			ln := l.launchers[name]
			if ln == nil || ln.srv == nil {
				return fmt.Errorf("%w: no launcher %q", harness.ErrStep, name)
			}
			return started(l.w.IDs[run], runtime, vendor, tool, source, ln.srv)
		})
	sc.Step(`^the launcher "(\w+)" logged the run "(\w+)"'s runner output `+outputPattern+`$`,
		func(name, run, want string) error {
			ln := l.launchers[name]
			if ln == nil || ln.srv == nil {
				return fmt.Errorf("%w: no launcher %q", harness.ErrStep, name)
			}
			return logged(l.w.IDs[run], want, ln.srv)
		})
}

// hostPhaseInputs gives a launcher's settings, in dir, the fixture's
// inputs by a host's means, and answers the process environment that
// holds the variables: launcher.whitelist_envs names them, mask_envs the
// write token, and the files are the host's, extra_mounts the shared one
// and ro_extra_mounts and wo_extra_mounts the scoped ones.
func hostPhaseInputs(dir string, settings map[string]any) ([]string, error) {
	files := map[string]string{"shared.txt": inputShared, "read.txt": inputReadFile, "write.txt": inputWriteFile}
	for name, content := range files {
		// The run's user reads the shared file through its bind mount; the
		// launcher reads the scoped ones and hands their contents over.
		mode := os.FileMode(0o600)
		if name == "shared.txt" {
			mode = 0o644
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content+"\n"), mode); err != nil {
			return nil, fmt.Errorf("phase-scoped input %s: %w", name, err)
		}
		if err := os.Chmod(filepath.Join(dir, name), mode); err != nil {
			return nil, fmt.Errorf("phase-scoped input %s: %w", name, err)
		}
	}
	settings["whitelist_envs"] = []string{"ro_PHASE_TOKEN", "wo_PHASE_TOKEN", "PHASE_CACHE", "PHASE_UNSET"}
	settings["mask_envs"] = []string{"wo_PHASE_TOKEN"}
	d, _ := settings["docker"].(map[string]any)
	d["extra_mounts"] = []string{filepath.Join(dir, "shared.txt") + ":" + inputSharedFile + ":ro"}
	d["ro_extra_mounts"] = []string{filepath.Join(dir, "read.txt") + ":" + inputKeysDir + "/read.txt"}
	d["wo_extra_mounts"] = []string{filepath.Join(dir, "write.txt") + ":" + inputKeysDir + "/write.txt"}
	return []string{
		"ro_PHASE_TOKEN=" + inputReadToken, "wo_PHASE_TOKEN=" + inputWriteToken, "PHASE_CACHE=" + inputCache,
	}, nil
}

// container checks the run's one container: labelled with vendor and
// tool, the shared file bound read-only, an in-memory directory at the
// scoped files', and no input value in its labels, environment or
// command line, which the handoff alone carries.
func (l *launcherSteps) container(ctx context.Context, run, vendor, tool string) error {
	host, err := l.w.Suite.Launchers(ctx)
	if err != nil {
		return err
	}
	c, err := host.RunContainer(ctx, l.w.IDs[run])
	if err != nil {
		return err //nolint:wrapcheck // names the run
	}
	if c.Labels[docker.LabelVendor] != vendor || c.Labels[docker.LabelTool] != tool {
		return fmt.Errorf("%w: the run %s's container is labelled %v", harness.ErrStep, run, c.Labels)
	}
	if !slices.ContainsFunc(c.Binds, func(b string) bool { return strings.HasSuffix(b, ":"+inputSharedFile+":ro") }) {
		return fmt.Errorf("%w: the run %s's container binds %q", harness.ErrStep, run, c.Binds)
	}
	if _, ok := c.Tmpfs[inputKeysDir]; !ok || len(c.Tmpfs) != 1 {
		return fmt.Errorf("%w: the run %s's container has the tmpfs mounts %v", harness.ErrStep, run, c.Tmpfs)
	}
	carried := strings.Join(slices.Concat(c.Env, c.Args, c.Binds), "\n")
	for k, v := range c.Labels {
		carried += "\n" + k + "=" + v
	}
	if strings.Contains(carried, "PHASE_") || slices.ContainsFunc(inputValues, func(v string) bool {
		return strings.Contains(carried, v)
	}) {
		return fmt.Errorf("%w: the run %s's container carries a phase-scoped input", harness.ErrStep, run)
	}
	return nil
}

// started checks that one of servers logged the container of the run
// whose ID is run started on runtime, executing vendor with tool from
// source, and that none logged a value of the fixture.
func started(run, runtime, vendor, tool, source string, servers ...*harness.Server) error {
	want := map[string]string{
		"run": run, "runtime": runtime, "vendor": vendor, "tool": tool, "tool_source": source,
	}
	found := false
	for _, srv := range servers {
		for _, out := range [][]byte{srv.Stdout(), srv.Stderr()} {
			if slices.ContainsFunc(maskedValues, func(v string) bool { return bytes.Contains(out, []byte(v)) }) {
				return fmt.Errorf("%w: a log holds a phase-scoped value", harness.ErrStep)
			}
		}
		lines := bufio.NewScanner(bytes.NewReader(srv.Stdout()))
		lines.Buffer(nil, 1<<20)
		for lines.Scan() {
			var line map[string]any
			if json.Unmarshal(lines.Bytes(), &line) != nil || line["msg"] != "run container started" || line["run"] != run {
				continue
			}
			found = true
			for k, v := range want {
				if line[k] != v {
					return fmt.Errorf("%w: the run's container started with %s %v, want %s", harness.ErrStep, k, line[k], v)
				}
			}
			if _, ok := line["image"].(string); !ok {
				return fmt.Errorf("%w: the run's container start names no image: %s", harness.ErrStep, lines.Bytes())
			}
		}
	}
	if !found {
		return fmt.Errorf("%w: no log line of the run %s's container start", harness.ErrStep, run)
	}
	return nil
}

// logged checks that servers logged, as the runner output of the run whose
// ID is run, the lines of want, a step's quoted text, one after another.
func logged(run, want string, servers ...*harness.Server) error {
	text, err := strconv.Unquote(`"` + want + `"`)
	if err != nil {
		return fmt.Errorf("%w: the text %q: %w", harness.ErrStep, want, err)
	}
	var out []string
	for _, srv := range servers {
		lines := bufio.NewScanner(bytes.NewReader(srv.Stdout()))
		lines.Buffer(nil, 1<<20)
		for lines.Scan() {
			var line struct {
				Msg  string `json:"msg"`
				Run  string `json:"run"`
				Line string `json:"line"`
			}
			if json.Unmarshal(lines.Bytes(), &line) == nil && line.Msg == "runner output" && line.Run == run {
				out = append(out, line.Line)
			}
		}
	}
	if got := strings.Join(out, "\n"); !strings.Contains(got, text) {
		return fmt.Errorf("%w: the run's runner output is %q", harness.ErrStep, got)
	}
	return nil
}

func (c *controllerSteps) registerPhaseInputs(sc *godog.ScenarioContext) {
	sc.Step(`^the Managed WorkerPool "(\w+)" of capacity (\d+) in the space "(\w+)", `+
		`with the phase-scoped inputs, is applied in "(\w+)"$`, c.withInputs)
	sc.Step(`^the run "(\w+)" ran in Pods labelled with the vendor "(\w+)" and the tool "(\w+)"$`, c.labelled)
	sc.Step(`^a worker controller logged the run "(\w+)"'s container on the runtime "(\w+)" `+
		`executing the vendor "(\w+)" with the tool "(\w+)" of the (release|image), and no phase-scoped value$`,
		func(run, runtime, vendor, tool, source string) error {
			return started(c.w.IDs[run], runtime, vendor, tool, source, slices.Concat(c.running, c.killed)...)
		})
	sc.Step(`^a worker controller logged the run "(\w+)"'s runner output `+outputPattern+`$`,
		func(run, want string) error {
			return logged(c.w.IDs[run], want, slices.Concat(c.running, c.killed)...)
		})
}

// withInputs applies a Managed WorkerPool whose environment and mounts
// are the fixture's by a cluster's means: the tokens and scoped files
// are keys of the Secret <name>-inputs, the cache and the shared file of
// the ConfigMap <name>-settings, in the pool's namespace.
func (c *controllerSteps) withInputs(ctx context.Context, name string, capacity int64, space, in string) error {
	ns := c.namespaces[in]
	if err := c.secret(ctx, ns, name+"-inputs", map[string][]byte{
		"read-token": []byte(inputReadToken), "write-token": []byte(inputWriteToken),
		"read.txt": []byte(inputReadFile + "\n"), "write.txt": []byte(inputWriteFile + "\n"),
	}); err != nil {
		return err
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-settings", Namespace: ns},
		Data:       map[string]string{"cache": inputCache, "shared.txt": inputShared + "\n"},
	}
	_, err := c.core.ConfigMaps(ns).Create(ctx, cm, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		_, err = c.core.ConfigMaps(ns).Update(ctx, cm, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("%w: write ConfigMap %s/%s: %w", harness.ErrStep, ns, cm.Name, err)
	}
	from := func(kind, object, key string) map[string]any {
		return map[string]any{"kind": kind, "name": object, "key": key}
	}
	inputs, settings := name+"-inputs", name+"-settings"
	return c.managedOn(ctx, name, capacity, space, in, "", func(spec, _ map[string]any) {
		spec["environment"] = []any{
			map[string]any{"name": "ro_PHASE_TOKEN", "exposure": "read", "from": from("Secret", inputs, "read-token")},
			map[string]any{"name": "wo_PHASE_TOKEN", "exposure": "write", "from": from("Secret", inputs, "write-token")},
			map[string]any{"name": "PHASE_CACHE", "exposure": "all", "from": from("ConfigMap", settings, "cache")},
		}
		spec["mounts"] = []any{
			map[string]any{
				"kind": "ConfigMap", "name": settings, "key": "shared.txt", "target": inputSharedFile,
				"exposure": "all", "readOnly": true,
			},
			map[string]any{
				"kind": "Secret", "name": inputs, "key": "read.txt", "target": inputKeysDir + "/read.txt",
				"exposure": "read", "readOnly": true,
			},
			map[string]any{
				"kind": "Secret", "name": inputs, "key": "write.txt", "target": inputKeysDir + "/write.txt",
				"exposure": "write", "readOnly": true,
			},
		}
	})
}

// labelled checks that every recorded Pod of the run is labelled with
// vendor and tool, mounts the shared file read-only from the pool's
// ConfigMap, and gives its runner the scoped directory in memory.
func (c *controllerSteps) labelled(run, vendor, tool string) error {
	pods := c.recorded(c.w.IDs[run])
	if len(pods) == 0 {
		return fmt.Errorf("%w: no Pod of the run %s was recorded", harness.ErrStep, run)
	}
	for _, p := range pods {
		if p.Labels[kubernetes.LabelVendor] != vendor || p.Labels[kubernetes.LabelTool] != tool {
			return fmt.Errorf("%w: the Pod %s is labelled %v", harness.ErrStep, p.Name, p.Labels)
		}
		var shared, scoped bool
		for _, m := range p.Spec.Containers[0].VolumeMounts {
			shared = shared || m.MountPath == inputSharedFile && m.ReadOnly
			scoped = scoped || m.MountPath == inputKeysDir && !m.ReadOnly
		}
		if !shared || !scoped {
			return fmt.Errorf("%w: the Pod %s's runner mounts %+v", harness.ErrStep, p.Name, p.Spec.Containers[0].VolumeMounts)
		}
	}
	return nil
}
