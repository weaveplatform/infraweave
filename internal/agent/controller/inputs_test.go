package controller

import (
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/weaveplatform/infraweave/internal/agent/executor"
	"github.com/weaveplatform/infraweave/internal/agent/launcher"
	"github.com/weaveplatform/infraweave/internal/agent/phaseinputs"
)

func TestUnitPoolInputsResolveFromThePoolsNamespace(t *testing.T) {
	core := k8sfake.NewClientset(
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "keys", Namespace: "home"},
			Data:       map[string][]byte{"read": []byte("r-secret"), "write": []byte("w-secret")},
			StringData: map[string]string{"string": "s"},
		},
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "settings", Namespace: "home"},
			Data:       map[string]string{"cache": "/cache"},
			BinaryData: map[string][]byte{"blob": []byte("b")},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "keys", Namespace: "other"},
			Data:       map[string][]byte{"read": []byte("other-namespace")},
		},
	).CoreV1()
	key := func(s string) *string { return &s }
	p := &poolInputs{core: core, namespace: "home"}
	p.set(PoolSpec{
		Environment: []Environment{
			{Name: "ro_TOKEN", Exposure: "read", From: ObjectKey{Kind: "Secret", Name: "keys", Key: "read"}},
			{Name: "wo_TOKEN", Exposure: "write", From: ObjectKey{Kind: "Secret", Name: "keys", Key: "write"}},
			{Name: "CACHE", Exposure: "all", From: ObjectKey{Kind: "ConfigMap", Name: "settings", Key: "cache"}},
			{Name: "STRING", Exposure: "all", From: ObjectKey{Kind: "Secret", Name: "keys", Key: "string"}},
		},
		Mounts: []Mount{
			{Kind: "PVC", Name: "data", Target: "/mnt/data", Exposure: "all", ReadOnly: true},
			{Kind: "Secret", Name: "keys", Key: key("read"), Target: "/mnt/keys/read", Exposure: "read", ReadOnly: true},
			{Kind: "ConfigMap", Name: "settings", Key: key("blob"), Target: "/mnt/keys/blob", Exposure: "write", ReadOnly: true},
		},
	})
	got, err := p.Resolve(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := launcher.Resolved{
		Inputs: phaseinputs.Inputs{
			Variables: []phaseinputs.Variable{
				{Name: "TOKEN", Exposure: phaseinputs.Read, Value: "r-secret", Sensitive: true},
				{Name: "TOKEN", Exposure: phaseinputs.Write, Value: "w-secret", Sensitive: true},
				{Name: "CACHE", Exposure: phaseinputs.All, Value: "/cache"},
				{Name: "STRING", Exposure: phaseinputs.All, Value: "s", Sensitive: true},
			},
			Files: []phaseinputs.File{
				{Target: "/mnt/keys/read", Exposure: phaseinputs.Read, Content: []byte("r-secret"), Sensitive: true},
				{Target: "/mnt/keys/blob", Exposure: phaseinputs.Write, Content: []byte("b")},
			},
		},
		Volumes: []executor.Volume{{Kind: "PVC", Name: "data", Target: "/mnt/data"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved:\n got %+v\nwant %+v", got, want)
	}

	for _, tc := range []struct {
		spec PoolSpec
		want string
	}{
		{PoolSpec{Environment: []Environment{{Name: "ro_X", Exposure: "write", From: ObjectKey{Kind: "Secret", Name: "keys", Key: "read"}}}}, "environment ro_X"},
		{PoolSpec{Environment: []Environment{
			{Name: "X", Exposure: "all", From: ObjectKey{Kind: "Secret", Name: "keys", Key: "read"}},
			{Name: "ro_X", Exposure: "read", From: ObjectKey{Kind: "Secret", Name: "keys", Key: "read"}},
		}}, "collides"},
		{PoolSpec{Environment: []Environment{{Name: "X", Exposure: "all", From: ObjectKey{Kind: "Secret", Name: "missing", Key: "read"}}}}, "the Secret missing"},
		{PoolSpec{Environment: []Environment{{Name: "X", Exposure: "all", From: ObjectKey{Kind: "Secret", Name: "keys", Key: "none"}}}}, "key none"},
		{PoolSpec{Environment: []Environment{{Name: "X", Exposure: "all", From: ObjectKey{Kind: "ConfigMap", Name: "missing", Key: "a"}}}}, "the ConfigMap missing"},
		{PoolSpec{Environment: []Environment{{Name: "X", Exposure: "all", From: ObjectKey{Kind: "ConfigMap", Name: "settings", Key: "none"}}}}, "key none"},
		{PoolSpec{Environment: []Environment{{Name: "X", Exposure: "all", From: ObjectKey{Kind: "PVC", Name: "data", Key: "a"}}}}, "the PVC data"},
		{PoolSpec{Mounts: []Mount{{Kind: "PVC", Name: "data", Target: "/mnt/data", Exposure: "read", ReadOnly: true}}}, "mount /mnt/data"},
		{PoolSpec{Mounts: []Mount{{Kind: "Secret", Name: "keys", Target: "/mnt/data", Exposure: "root", ReadOnly: true}}}, "exposure"},
		{PoolSpec{Mounts: []Mount{{Kind: "Secret", Name: "keys", Key: key("none"), Target: "/mnt/data/k", Exposure: "write", ReadOnly: true}}}, "key none"},
	} {
		p.set(tc.spec)
		_, err := p.Resolve(t.Context())
		if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "-secret") {
			t.Errorf("%+v: %v", tc.spec, err)
		}
	}
}
