package workerchart_test

import (
	"bytes"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/pflag"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/common"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	kubefake "helm.sh/helm/v4/pkg/kube/fake"
	release "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage"
	"helm.sh/helm/v4/pkg/storage/driver"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/yaml"

	"github.com/weaveplatform/infraweave/internal/agent/controller"
)

const (
	releaseNamespace = "infraweave"
	digest           = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	poolAccount      = "01KACME0000000000000000003"
	poolID           = "01KQ0000000000000000000001"
)

var kubeVersion = func() *common.KubeVersion {
	v, err := common.ParseKubeVersion("v1.37.2")
	if err != nil {
		panic(err)
	}
	return v
}()

// values are a release's values: two watch namespaces, a Managed pool in
// one and an Existing pool in the other, and every reference set.
func values(namespaces ...string) map[string]any {
	if len(namespaces) == 0 {
		namespaces = []string{"pools-a", "pools-b"}
	}
	pod := map[string]any{
		"resources": map[string]any{
			"requests": map[string]any{"cpu": "500m", "memory": "1Gi", "ephemeral-storage": "2Gi"},
			"limits":   map[string]any{"cpu": "2", "memory": "4Gi", "ephemeral-storage": "12Gi"},
		},
		"storageSize": "12Gi",
	}
	return map[string]any{
		"clusterID":       "cluster-one",
		"watchNamespaces": toAny(namespaces),
		"controller":      map[string]any{"image": map[string]any{"digest": digest}},
		"toolMirror":      "https://mirror.example/tools",
		"managementCredentialsRef": map[string]any{
			"name": "management", "idKey": "id", "secretKey": "secret",
		},
		"trust":                   map[string]any{"caSecretRef": map[string]any{"name": "trust", "key": "ca.pem"}},
		"initializationPolicyRef": map[string]any{"kind": "ConfigMap", "name": "policy", "key": "initialization.rego"},
		"pools": []any{
			map[string]any{"name": "build", "namespace": namespaces[0], "spec": map[string]any{
				"endpoint": "https://infraweave.example", "account": poolAccount, "space": "team", "mode": "Managed",
				"credentialsRef": map[string]any{"name": "build-credentials"}, "capacity": 2, "name": "Build", "pod": pod,
			}},
			map[string]any{"name": "existing", "namespace": namespaces[len(namespaces)-1], "spec": map[string]any{
				"endpoint": "https://infraweave.example", "account": poolAccount, "space": "team", "mode": "Existing",
				"poolID": poolID, "credentialsRef": map[string]any{"name": "existing-credentials"}, "capacity": 1, "pod": pod,
			}},
		},
	}
}

func toAny(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

// render renders the chart offline as name in namespace, as helm template
// does, with its CRDs and hooks.
func render(t *testing.T, name, namespace string, vals map[string]any) (*release.Release, error) {
	t.Helper()
	ch, err := loader.Load(chartDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &action.Configuration{
		Releases:     storage.Init(driver.NewMemory()),
		KubeClient:   &kubefake.PrintingKubeClient{Out: io.Discard},
		Capabilities: common.DefaultCapabilities,
	}
	in := action.NewInstall(cfg)
	in.DryRunStrategy = action.DryRunClient
	in.ReleaseName, in.Namespace, in.Replace, in.IncludeCRDs = name, namespace, true, true
	in.KubeVersion = kubeVersion
	r, err := in.Run(ch, vals)
	if err != nil {
		return nil, err //nolint:wrapcheck // the test reads Helm's error
	}
	rel, ok := r.(*release.Release)
	if !ok {
		t.Fatalf("release = %T", r)
	}
	return rel, nil
}

// object is one rendered manifest.
type object struct {
	kind, namespace, name string
	raw                   []byte
	hook                  bool
}

func objects(t *testing.T, rel *release.Release) []object {
	t.Helper()
	var out []object
	add := func(manifest string, hook bool) {
		for doc := range strings.SplitSeq(manifest, "\n---") {
			if strings.TrimSpace(strings.TrimPrefix(doc, "---")) == "" {
				continue
			}
			var meta struct {
				Kind     string `json:"kind"`
				Metadata struct {
					Name      string `json:"name"`
					Namespace string `json:"namespace"`
				} `json:"metadata"`
			}
			if err := yaml.Unmarshal([]byte(doc), &meta); err != nil {
				t.Fatalf("%v: %s", err, doc)
			}
			if meta.Kind == "" {
				continue
			}
			out = append(out, object{meta.Kind, meta.Metadata.Namespace, meta.Metadata.Name, []byte(doc), hook})
		}
	}
	add(rel.Manifest, false)
	for _, h := range rel.Hooks {
		add(h.Manifest, true)
	}
	return out
}

// decode decodes raw strictly into a typed object of client-go's scheme,
// refusing an unknown or duplicated field as kubeconform would.
var strict = serializer.NewCodecFactory(scheme.Scheme, serializer.EnableStrict).UniversalDeserializer()

func decode[T runtime.Object](t *testing.T, o object) T {
	t.Helper()
	obj, _, err := strict.Decode(o.raw, nil, nil)
	if err != nil {
		t.Fatalf("%s %s/%s: %v", o.kind, o.namespace, o.name, err)
	}
	typed, ok := obj.(T)
	if !ok {
		t.Fatalf("%s %s/%s decodes as %T", o.kind, o.namespace, o.name, obj)
	}
	return typed
}

func find(t *testing.T, objs []object, kind, namespace, name string) object {
	t.Helper()
	for _, o := range objs {
		if o.kind == kind && o.namespace == namespace && o.name == name {
			return o
		}
	}
	t.Fatalf("no %s %s/%s rendered", kind, namespace, name)
	return object{}
}

func TestUnitWorkerChartLints(t *testing.T) {
	lint := action.NewLint()
	lint.Strict, lint.Namespace, lint.KubeVersion = true, releaseNamespace, kubeVersion
	result := lint.Run([]string{chartDir}, values())
	for _, m := range result.Messages {
		t.Logf("%s", m.Error())
	}
	if len(result.Errors) > 0 {
		t.Fatalf("helm lint: %v", result.Errors)
	}
	bare := action.NewLint()
	bare.KubeVersion = kubeVersion
	if result := bare.Run([]string{chartDir}, nil); len(result.Errors) == 0 {
		t.Fatal("helm lint passes the chart without a cluster ID or an image digest")
	}
}

func TestUnitWorkerChartRendersEveryKindStrictly(t *testing.T) {
	rel, err := render(t, "alpha", releaseNamespace, values())
	if err != nil {
		t.Fatal(err)
	}
	objs := objects(t, rel)
	kinds := map[string]int{}
	for _, o := range objs {
		kinds[o.kind]++
		switch o.kind {
		case "ClusterRole", "ClusterRoleBinding", "Secret":
			t.Fatalf("the chart renders a %s %s", o.kind, o.name)
		case "CustomResourceDefinition":
			continue
		case "WorkerPool":
			var wp controller.WorkerPool
			if err := yaml.UnmarshalStrict(o.raw, &wp); err != nil {
				t.Fatalf("WorkerPool %s: %v", o.name, err)
			}
			if wp.Labels["app.kubernetes.io/instance"] != "alpha" || wp.Labels["app.kubernetes.io/name"] != "infraweave-workers" {
				t.Fatalf("WorkerPool %s is labelled %v", o.name, wp.Labels)
			}
		default:
			decode[runtime.Object](t, o)
		}
	}
	want := map[string]int{
		"CustomResourceDefinition": 2, "ServiceAccount": 2, "ConfigMap": 1, "Deployment": 1, "PodDisruptionBudget": 1,
		"Role": 5, "RoleBinding": 5, "NetworkPolicy": 3, "WorkerPool": 2, "Job": 1, "Lease": 1,
	}
	if !maps.Equal(kinds, want) {
		t.Fatalf("rendered kinds = %v, want %v", kinds, want)
	}
}

func TestUnitWorkerChartScopesTheControllerToItsNamespaces(t *testing.T) {
	rel, err := render(t, "alpha", releaseNamespace, values())
	if err != nil {
		t.Fatal(err)
	}
	objs := objects(t, rel)
	for _, ns := range []string{"pools-a", "pools-b"} {
		role := decode[*rbacv1.Role](t, find(t, objs, "Role", ns, "infraweave-worker-controller"))
		for _, rule := range role.Rules {
			if slices.Contains(rule.Resources, "secrets") && (slices.Contains(rule.Verbs, "list") || slices.Contains(rule.Verbs, "watch")) {
				t.Fatalf("the controller lists Secrets in %s", ns)
			}
			if slices.Contains(rule.Verbs, "*") || slices.Contains(rule.Resources, "*") {
				t.Fatalf("the controller holds a wildcard rule in %s: %+v", ns, rule)
			}
		}
		binding := decode[*rbacv1.RoleBinding](t, find(t, objs, "RoleBinding", ns, "infraweave-worker-controller"))
		if len(binding.Subjects) != 1 || binding.Subjects[0].Name != "alpha-worker-controller" || binding.Subjects[0].Namespace != releaseNamespace {
			t.Fatalf("the controller binding in %s names %+v", ns, binding.Subjects)
		}
		runs := decode[*networkingv1.NetworkPolicy](t, find(t, objs, "NetworkPolicy", ns, "infraweave-worker-runs"))
		if len(runs.Spec.Ingress) != 0 || runs.Spec.PodSelector.MatchLabels["app.kubernetes.io/managed-by"] != "infraweave-worker-controller" {
			t.Fatalf("the run Pods' policy in %s = %+v", ns, runs.Spec)
		}
	}
	lease := decode[*rbacv1.Role](t, find(t, objs, "Role", releaseNamespace, "alpha-worker-controller-lease"))
	if len(lease.Rules) != 1 || !slices.Equal(lease.Rules[0].ResourceNames, []string{"alpha-worker-controller"}) ||
		slices.Contains(lease.Rules[0].Verbs, "create") {
		t.Fatalf("the Lease role = %+v", lease.Rules)
	}
}

func TestUnitWorkerChartHardensTheController(t *testing.T) {
	rel, err := render(t, "alpha", releaseNamespace, values())
	if err != nil {
		t.Fatal(err)
	}
	objs := objects(t, rel)
	d := decode[*appsv1.Deployment](t, find(t, objs, "Deployment", releaseNamespace, "alpha-worker-controller"))
	pod := d.Spec.Template.Spec
	if *d.Spec.Replicas != 2 || d.Spec.Strategy.RollingUpdate.MaxUnavailable.IntValue() != 0 {
		t.Fatalf("replicas and strategy = %d %+v", *d.Spec.Replicas, d.Spec.Strategy)
	}
	c := pod.Containers[0]
	if c.Image != "ghcr.io/weaveplatform/infraweave-worker@"+digest {
		t.Fatalf("image = %s", c.Image)
	}
	sc := c.SecurityContext
	if !*pod.SecurityContext.RunAsNonRoot || *pod.SecurityContext.RunAsUser != 1983 || *sc.AllowPrivilegeEscalation ||
		!*sc.ReadOnlyRootFilesystem || !slices.Equal(sc.Capabilities.Drop, []corev1.Capability{"ALL"}) ||
		pod.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("the controller is not restricted: %+v %+v", pod.SecurityContext, sc)
	}
	if c.ReadinessProbe.HTTPGet.Path != "/readyz" || c.LivenessProbe.HTTPGet.Path != "/livez" {
		t.Fatalf("probes = %+v %+v", c.ReadinessProbe, c.LivenessProbe)
	}
	if len(c.Env) != 1 || c.Env[0].Value != "fips140=on" {
		t.Fatalf("env = %+v", c.Env)
	}
	if c.Resources.Limits.Memory().IsZero() || c.Resources.Requests.StorageEphemeral().IsZero() {
		t.Fatalf("resources = %+v", c.Resources)
	}
	for _, v := range pod.Volumes {
		if v.HostPath != nil {
			t.Fatalf("the controller mounts the host path %s", v.HostPath.Path)
		}
	}
	if d.Spec.Template.Annotations["infraweave.io/config-sha256"] == "" {
		t.Fatal("the Pods do not roll when the configuration changes")
	}
}

func TestUnitWorkerChartConfiguresTheController(t *testing.T) {
	rel, err := render(t, "alpha", releaseNamespace, values())
	if err != nil {
		t.Fatal(err)
	}
	cm := decode[*corev1.ConfigMap](t, find(t, objects(t, rel), "ConfigMap", releaseNamespace, "alpha-worker-controller"))
	config := cm.Data["worker-controller.yaml"]
	path := filepath.Join(t.TempDir(), "worker-controller.yaml")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	flags := pflag.NewFlagSet("worker-controller", pflag.ContinueOnError)
	controller.ConfigurationFlags(flags)
	if err := flags.Parse([]string{"--config", path}); err != nil {
		t.Fatal(err)
	}
	s, err := controller.LoadSettings(flags)
	if err != nil {
		t.Fatalf("the controller refuses the chart's configuration: %v\n%s", err, config)
	}
	if s.ClusterID != "cluster-one" || !slices.Equal(s.WatchNamespaces, []string{"pools-a", "pools-b"}) ||
		s.LeaderElection.Name != "alpha-worker-controller" || s.LeaderElection.Namespace != releaseNamespace ||
		s.BootstrapImage != "ghcr.io/weaveplatform/infraweave-worker@"+digest || s.CAFile == "" || s.InitializationPolicy == "" ||
		s.ManagementCredentialsRef == nil || s.ManagementCredentialsRef.Name != "management" || s.ToolMirror != "https://mirror.example/tools" {
		t.Fatalf("settings = %+v", s)
	}
	bootstrap := values()
	bootstrap["bootstrapImage"] = map[string]any{"repository": "registry.example/infraweave", "digest": digest}
	rel, err = render(t, "alpha", releaseNamespace, bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	cm = decode[*corev1.ConfigMap](t, find(t, objects(t, rel), "ConfigMap", releaseNamespace, "alpha-worker-controller"))
	if !strings.Contains(cm.Data["worker-controller.yaml"], `bootstrap_image: "registry.example/infraweave@`+digest+`"`) {
		t.Fatalf("an explicit bootstrap image is not configured: %s", cm.Data["worker-controller.yaml"])
	}
}

func TestUnitWorkerChartUninstallsThroughItsController(t *testing.T) {
	rel, err := render(t, "alpha", releaseNamespace, values())
	if err != nil {
		t.Fatal(err)
	}
	objs := objects(t, rel)
	job := find(t, objs, "Job", releaseNamespace, "alpha-worker-uninstall")
	if !job.hook {
		t.Fatal("the uninstall Job is not a hook")
	}
	j := decode[*batchv1.Job](t, job)
	args := j.Spec.Template.Spec.Containers[0].Args
	if !slices.Equal(args[:2], []string{"worker-controller", "uninstall"}) ||
		!slices.Contains(args, "app.kubernetes.io/name=infraweave-workers,app.kubernetes.io/instance=alpha") {
		t.Fatalf("the uninstall Job runs %v", args)
	}
	for _, h := range rel.Hooks {
		if len(h.Events) != 1 || h.Events[0] != release.HookPreDelete {
			t.Fatalf("the hook %s %s runs on %v", h.Kind, h.Name, h.Events)
		}
	}
	for _, ns := range []string{"pools-a", "pools-b"} {
		role := decode[*rbacv1.Role](t, find(t, objs, "Role", ns, "alpha-worker-uninstall"))
		if !slices.Equal(role.Rules[0].Verbs, []string{"get", "list", "delete"}) {
			t.Fatalf("the uninstall role in %s = %+v", ns, role.Rules)
		}
	}
}

func TestUnitTwoWorkerChartReleasesShareNoName(t *testing.T) {
	alpha, err := render(t, "alpha", releaseNamespace, values("alpha-pools"))
	if err != nil {
		t.Fatal(err)
	}
	beta, err := render(t, "beta", releaseNamespace, values("beta-pools"))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, o := range objects(t, alpha) {
		names[o.kind+" "+o.namespace+"/"+o.name] = true
	}
	for _, o := range objects(t, beta) {
		if o.kind != "CustomResourceDefinition" && names[o.kind+" "+o.namespace+"/"+o.name] {
			t.Fatalf("both releases render %s %s/%s", o.kind, o.namespace, o.name)
		}
	}
	overlap, err := render(t, "beta", releaseNamespace, values("alpha-pools"))
	if err != nil {
		t.Fatal(err)
	}
	shared := false
	for _, o := range objects(t, overlap) {
		shared = shared || o.kind == "Role" && names["Role "+o.namespace+"/"+o.name]
	}
	if !shared {
		t.Fatal("two releases watching one namespace render no object Helm refuses to share")
	}
}

func TestUnitWorkerChartRefusesInvalidValues(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(map[string]any)
		want   string
	}{
		"an unknown key":             {func(v map[string]any) { v["privileged"] = true }, "privileged"},
		"an unknown controller key":  {func(v map[string]any) { ctl(v)["securityContext"] = map[string]any{} }, "securityContext"},
		"an unknown pool spec key":   {func(v map[string]any) { spec(v)["pod"].(map[string]any)["privileged"] = true }, "privileged"},
		"an image without a digest":  {func(v map[string]any) { ctl(v)["image"] = map[string]any{"digest": ""} }, "digest"},
		"a tag for a digest":         {func(v map[string]any) { ctl(v)["image"] = map[string]any{"digest": "latest"} }, "digest"},
		"no cluster ID":              {func(v map[string]any) { delete(v, "clusterID") }, "clusterID"},
		"no watch namespace":         {func(v map[string]any) { v["watchNamespaces"] = []any{} }, "watchNamespaces"},
		"a repeated watch namespace": {func(v map[string]any) { v["watchNamespaces"] = []any{"pools-a", "pools-a"} }, "watchNamespaces"},
		"no replica":                 {func(v map[string]any) { ctl(v)["replicas"] = 0 }, "replicas"},
		"a FIPS mode off":            {func(v map[string]any) { ctl(v)["fips140"] = "off" }, "fips140"},
		"a resource quantity":        {func(v map[string]any) { ctl(v)["cacheSizeLimit"] = "lots" }, "cacheSizeLimit"},
		"a tool mirror scheme":       {func(v map[string]any) { v["toolMirror"] = "ftp://mirror.example" }, "toolMirror"},
		"an egress CIDR": {func(v map[string]any) {
			v["networkPolicy"] = map[string]any{"runEgress": []any{map[string]any{"cidr": "everywhere"}}}
		}, "cidr"},
		"a long initialization":    {func(v map[string]any) { v["initializationTimeoutSeconds"] = 61 }, "initializationTimeoutSeconds"},
		"a bootstrap digest alone": {func(v map[string]any) { v["bootstrapImage"] = map[string]any{"digest": digest} }, "bootstrapImage"},
		"an unwatched pool":        {func(v map[string]any) { pool(v)["namespace"] = "elsewhere" }, "watchNamespaces does not name"},
		"a pool named twice":       {func(v map[string]any) { v["pools"] = append(v["pools"].([]any), pool(v)) }, "named twice"},
		"no management key":        {func(v map[string]any) { v["managementCredentialsRef"] = nil }, "Managed pools need"},
		"a management key one key": {func(v map[string]any) { v["managementCredentialsRef"].(map[string]any)["secretKey"] = "id" }, "distinct"},
		"a Public pool's management": {func(v map[string]any) {
			s := spec(v)
			s["mode"], s["poolID"] = "Public", poolID
			delete(s, "account")
			delete(s, "space")
			delete(s, "name")
			v["pools"] = []any{pool(v)}
		}, "only Public pools"},
		"an unknown policy kind": {func(v map[string]any) {
			v["initializationPolicyRef"] = map[string]any{"kind": "File", "name": "policy", "key": "p"}
		}, "initializationPolicyRef"},
	} {
		t.Run(name, func(t *testing.T) {
			v := values()
			tc.change(v)
			_, err := render(t, "alpha", releaseNamespace, v)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("render = %v, want an error naming %q", err, tc.want)
			}
		})
	}
}

func ctl(v map[string]any) map[string]any  { return v["controller"].(map[string]any) }
func pool(v map[string]any) map[string]any { return v["pools"].([]any)[0].(map[string]any) }
func spec(v map[string]any) map[string]any { return pool(v)["spec"].(map[string]any) }

func TestUnitWorkerChartRendersWithoutOptionalInputs(t *testing.T) {
	v := values()
	for _, key := range []string{"trust", "initializationPolicyRef", "toolMirror"} {
		delete(v, key)
	}
	v["networkPolicy"] = map[string]any{"enabled": false}
	ctl(v)["replicas"] = 1
	rel, err := render(t, "alpha", releaseNamespace, v)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	for _, o := range objects(t, rel) {
		if o.kind == "NetworkPolicy" || o.kind == "PodDisruptionBudget" {
			t.Fatalf("a disabled %s is rendered", o.kind)
		}
		buf.Write(o.raw)
	}
	for _, path := range []string{"ca_file", "initialization_policy", "tool_mirror"} {
		if strings.Contains(buf.String(), path) {
			t.Fatalf("an unset %s is configured", path)
		}
	}
}
