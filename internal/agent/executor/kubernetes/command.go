package kubernetes

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/weaveplatform/infraweave/internal/agent/executor"
)

// An initialization command's helper Pod: its labels, which differ from a
// run Pod's so Find never answers a helper as a run, and its container.
const (
	LabelCommandWorker = "workers.infraweave.io/command-worker"
	LabelCommandRun    = "workers.infraweave.io/command-run"
	CommandContainer   = "command"
)

// CommandCommand is the helper container's fixed entrypoint, the copied
// infraweave executable serving the private request on its standard input.
var CommandCommand = []string{BinaryDir + "/infraweave", "runner", "command"}

// commandDeadline is a helper Pod's active deadline when its call's
// context has none, the bound of one call.
const commandDeadline = 60 * time.Second

// commandWorkspace bounds the empty workspace a helper's bootstrap
// extracts; the helper container does not mount it.
var commandWorkspace = resource.MustParse("16Mi")

var _ executor.Commander = (*Executor)(nil)

// Command runs c's helper in a Pod of its own in the worker's namespace,
// owned by the worker's resource, with a run Pod's fixed security and the
// pool's resources, placement and pull secrets. Its bootstrap container
// receives only the infraweave executable and an empty workspace; the
// helper container, of c.Image, mounts that executable read-only and
// nothing else, and starts it as runner command. The Pod's active deadline
// is the call's remaining time. Its standard input alone carries
// c.Request, its standard output goes to c.Output, and the Pod is
// deleted however the call ends. An image that cannot be pulled is
// executor.ErrImage.
//
// ref: decision 0009 § "The worker-side initialization policy"
func (e *Executor) Command(ctx context.Context, cmd executor.Command) (err error) {
	c := e.config()
	staged, m, err := stage(c, executor.Job{Worker: cmd.Worker, Run: cmd.Run, StateVersion: cmd.StateVersion})
	if err != nil {
		return err
	}
	defer os.Remove(staged)
	remaining := commandDeadline
	if deadline, ok := ctx.Deadline(); ok {
		remaining = time.Until(deadline)
	}
	p, err := e.core.Pods(c.Namespace).Create(ctx, commandPod(c, cmd, remaining), metav1.CreateOptions{FieldValidation: metav1.FieldValidationStrict})
	if err != nil {
		return fmt.Errorf("kubernetes: create run %s's initialization command: %w", cmd.Run, err)
	}
	r := &run{e: e, c: c, name: p.Name, uid: p.UID, run: cmd.Run, copied: closed()}
	defer func() {
		if derr := r.delete(context.WithoutCancel(ctx), 0); derr != nil {
			err = errors.Join(err, derr)
		}
	}()
	if err := e.prepare(ctx, r, m, staged, nil); err != nil {
		return err
	}
	if err := e.await(ctx, r, running(CommandContainer)); err != nil {
		return err
	}
	out := cmd.Output
	if out == nil {
		out = io.Discard
	}
	// The helper writes nothing to standard error but its own failure,
	// which its missing result already reports.
	if err := e.streams.Attach(ctx, c.Namespace, r.name, CommandContainer, bytes.NewReader(cmd.Request), out, io.Discard); err != nil {
		return fmt.Errorf("kubernetes: run %s's initialization command: %w", cmd.Run, err)
	}
	return nil
}

// commandPod is c's helper Pod under c, whose active deadline is
// remaining, at least a second.
func commandPod(c Config, cmd executor.Command, remaining time.Duration) *corev1.Pod {
	no, yes := false, true
	uid := UID
	drop := &corev1.SecurityContext{
		AllowPrivilegeEscalation: &no, Privileged: &no, RunAsNonRoot: &yes,
		Capabilities:           &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		ReadOnlyRootFilesystem: &yes,
	}
	helper := drop.DeepCopy()
	// Verifiers write their caches under the user's home, as tools do.
	helper.ReadOnlyRootFilesystem = &no
	deadline := max(int64(math.Ceil(remaining.Seconds())), 1)
	bin, ws := binaryLimit.DeepCopy(), commandWorkspace.DeepCopy()
	owner := c.Owner
	owner.Controller, owner.BlockOwnerDeletion = &yes, nil
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: commandPodName(cmd), Namespace: c.Namespace,
			Labels: map[string]string{
				LabelCommandWorker: cmd.Worker, LabelCommandRun: cmd.Run, "app.kubernetes.io/managed-by": "infraweave-worker-controller",
			},
			OwnerReferences: []metav1.OwnerReference{owner},
		},
		Spec: corev1.PodSpec{
			RestartPolicy:                corev1.RestartPolicyNever,
			AutomountServiceAccountToken: &no,
			EnableServiceLinks:           &no,
			ShareProcessNamespace:        &no,
			ActiveDeadlineSeconds:        &deadline,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsUser: &uid, RunAsGroup: &uid, FSGroup: &uid, RunAsNonRoot: &yes,
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			NodeSelector:     c.NodeSelector,
			Tolerations:      c.Tolerations,
			ImagePullSecrets: c.ImagePullSecrets,
			Volumes: []corev1.Volume{
				{Name: volumeBinary, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: &bin}}},
				{Name: volumeWorkspace, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: &ws}}},
			},
			InitContainers: []corev1.Container{{
				Name:  BootstrapContainer,
				Image: c.BootstrapImage,
				Command: []string{
					BootstrapPath, "runner", "bootstrap", "--run", cmd.Run,
					"--state-version", strconv.FormatInt(cmd.StateVersion, 10), "--worker", cmd.Worker,
				},
				Stdin: true, StdinOnce: true,
				VolumeMounts: []corev1.VolumeMount{
					{Name: volumeBinary, MountPath: BinaryDir},
					{Name: volumeWorkspace, MountPath: Workspace},
				},
				Resources:                c.Resources,
				SecurityContext:          drop,
				ImagePullPolicy:          corev1.PullIfNotPresent,
				TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
			}},
			Containers: []corev1.Container{{
				Name:            CommandContainer,
				Image:           cmd.Image,
				Command:         CommandCommand,
				Stdin:           true,
				StdinOnce:       true,
				VolumeMounts:    []corev1.VolumeMount{{Name: volumeBinary, MountPath: BinaryDir, ReadOnly: true}},
				Resources:       c.Resources,
				SecurityContext: helper,
				ImagePullPolicy: corev1.PullIfNotPresent,
			}},
		},
	}
}

// commandPodName names a helper Pod for its run, state version and call,
// with a random suffix, so a call repeated after a restart never meets a
// Pod still being deleted.
func commandPodName(cmd executor.Command) string {
	suffix := make([]byte, 3)
	_, _ = rand.Read(suffix)
	return "iw-cmd-" + strings.ToLower(cmd.Run) + "-" + strconv.FormatInt(cmd.StateVersion, 10) + "-" +
		strconv.Itoa(cmd.Call) + "-" + hex.EncodeToString(suffix)
}

// sweep deletes worker's helper Pods that a controller stopped mid-call
// left behind; no helper outlives its call otherwise.
func (e *Executor) sweep(ctx context.Context, c Config, worker string) error {
	list, err := e.core.Pods(c.Namespace).List(ctx, metav1.ListOptions{LabelSelector: LabelCommandWorker + "=" + worker})
	if err != nil {
		return fmt.Errorf("kubernetes: list worker %s's initialization commands: %w", worker, err)
	}
	for _, p := range list.Items {
		r := &run{e: e, c: c, name: p.Name, uid: p.UID, run: p.Labels[LabelCommandRun]}
		if err := r.delete(ctx, 0); err != nil {
			return err
		}
	}
	return nil
}
