package kubernetes

import (
	"maps"
	"math"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/weaveplatform/infraweave/internal/agent/bootstrap"
	"github.com/weaveplatform/infraweave/internal/agent/executor"
)

// The run Pod's contract with its images. The bootstrap image is the
// same-version infraweave image, whose executable is at BootstrapPath;
// the runner image needs a user with UID and GID 1983 and runs unchanged,
// its entrypoint replaced by the copied executable at BinaryDir, which
// is where the Docker executor mounts it too, in Workspace.
const (
	BootstrapContainer       = "bootstrap"
	RunnerContainer          = "runner"
	BootstrapPath            = "/usr/local/bin/infraweave"
	BinaryDir                = bootstrap.BinaryDir
	ToolsDir                 = bootstrap.ToolsDir
	Workspace                = bootstrap.WorkspaceDir
	UID                int64 = 1983
)

// The labels that find a worker's Pods again, and the annotation the
// executor sets before it hands a Pod's runner its handoff, which tells
// a restarted controller that the Pod may hold a started run.
const (
	LabelWorker       = "workers.infraweave.io/worker"
	LabelRun          = "workers.infraweave.io/run"
	AnnotationHandoff = "workers.infraweave.io/handoff"
	handedOff         = "delivered"
)

// The emptyDir volumes of a run Pod. The binary and tools volumes hold one
// executable and at most two verified tools, MaxTool each.
const (
	volumeBinary    = "binary"
	volumeTools     = "tools"
	volumeWorkspace = "workspace"
)

var (
	binaryLimit = resource.MustParse("512Mi")
	toolsLimit  = resource.MustParse("1100Mi")
)

// PodName names the Pod of run's assignment at stateVersion: a confirmed
// run's apply after its plan gets a new Pod, never the plan's again.
func PodName(run string, stateVersion int64) string {
	return "iw-run-" + strings.ToLower(run) + "-" + strconv.FormatInt(stateVersion, 10)
}

// pod is the run Pod of j under c, mounting each of tools over its
// image's /bin/<name>. Its fixed security context, commands and volumes
// are not configurable: a pool sets only resources, storage, placement
// and pull secrets.
func pod(c Config, j executor.Job, tools []executor.Tool) *corev1.Pod {
	no, yes := false, true
	uid := UID
	drop := &corev1.SecurityContext{
		AllowPrivilegeEscalation: &no, Privileged: &no, RunAsNonRoot: &yes,
		Capabilities:           &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		ReadOnlyRootFilesystem: &yes,
	}
	runner := drop.DeepCopy()
	// Tools write their caches and plugins under the user's home.
	runner.ReadOnlyRootFilesystem = &no
	mounts := make([]corev1.VolumeMount, 0, 2+len(tools))
	mounts = append(mounts,
		corev1.VolumeMount{Name: volumeBinary, MountPath: BinaryDir, ReadOnly: true},
		corev1.VolumeMount{Name: volumeWorkspace, MountPath: Workspace},
	)
	for _, t := range tools {
		mounts = append(mounts, corev1.VolumeMount{Name: volumeTools, MountPath: "/bin/" + t.Name, SubPath: t.Name, ReadOnly: true})
	}
	selector := maps.Clone(c.NodeSelector)
	if len(tools) > 0 {
		if selector == nil {
			selector = map[string]string{}
		}
		selector[corev1.LabelArchStable] = c.Arch
	}
	var deadline *int64
	if c.ActiveDeadline > 0 {
		s := int64(math.Ceil(c.ActiveDeadline.Seconds()))
		deadline = &s
	}
	workspace := &corev1.EmptyDirVolumeSource{}
	if !c.StorageSize.IsZero() {
		size := c.StorageSize.DeepCopy()
		workspace.SizeLimit = &size
	}
	bin, tl := binaryLimit.DeepCopy(), toolsLimit.DeepCopy()
	// The owner reference does not block its owner's deletion, which would
	// need the worker's finalizers subresource: the worker's own
	// finalizer holds it until its Pods are handled.
	owner := c.Owner
	owner.Controller, owner.BlockOwnerDeletion = &yes, nil
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: PodName(j.Run, j.StateVersion), Namespace: c.Namespace,
			Labels: map[string]string{
				LabelWorker: j.Worker, LabelRun: j.Run, "app.kubernetes.io/managed-by": "infraweave-worker-controller",
			},
			OwnerReferences: []metav1.OwnerReference{owner},
		},
		Spec: corev1.PodSpec{
			RestartPolicy:                corev1.RestartPolicyNever,
			AutomountServiceAccountToken: &no,
			EnableServiceLinks:           &no,
			ShareProcessNamespace:        &no,
			ActiveDeadlineSeconds:        deadline,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsUser: &uid, RunAsGroup: &uid, FSGroup: &uid, RunAsNonRoot: &yes,
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			NodeSelector:     selector,
			Tolerations:      c.Tolerations,
			ImagePullSecrets: c.ImagePullSecrets,
			Volumes: []corev1.Volume{
				{Name: volumeBinary, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: &bin}}},
				{Name: volumeTools, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: &tl}}},
				{Name: volumeWorkspace, VolumeSource: corev1.VolumeSource{EmptyDir: workspace}},
			},
			InitContainers: []corev1.Container{{
				Name:  BootstrapContainer,
				Image: c.BootstrapImage,
				Command: []string{
					BootstrapPath, "runner", "bootstrap", "--run", j.Run,
					"--state-version", strconv.FormatInt(j.StateVersion, 10), "--worker", j.Worker,
				},
				Stdin: true, StdinOnce: true,
				VolumeMounts: []corev1.VolumeMount{
					{Name: volumeBinary, MountPath: BinaryDir},
					{Name: volumeTools, MountPath: ToolsDir},
					{Name: volumeWorkspace, MountPath: Workspace},
				},
				Resources:                c.Resources,
				SecurityContext:          drop,
				ImagePullPolicy:          corev1.PullIfNotPresent,
				TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
			}},
			Containers: []corev1.Container{{
				Name:            RunnerContainer,
				Image:           j.Image,
				Command:         []string{BinaryDir + "/infraweave", "runner"},
				WorkingDir:      Workspace,
				Stdin:           true,
				StdinOnce:       true,
				VolumeMounts:    mounts,
				Resources:       c.Resources,
				SecurityContext: runner,
				ImagePullPolicy: corev1.PullIfNotPresent,
			}},
		},
	}
}
