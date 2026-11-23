package phaseinputs_test

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/agent/phaseinputs"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

func TestUnitPhaseInputsName(t *testing.T) {
	for _, tt := range []struct {
		in, name string
		e        phaseinputs.Exposure
	}{
		{"ro_TOKEN", "TOKEN", phaseinputs.Read},
		{"wo_TOKEN", "TOKEN", phaseinputs.Write},
		{"TOKEN", "TOKEN", phaseinputs.All},
		{"RO_TOKEN", "RO_TOKEN", phaseinputs.All},
	} {
		if name, e := phaseinputs.Name(tt.in); name != tt.name || e != tt.e {
			t.Errorf("Name(%q) = %q, %q", tt.in, name, e)
		}
	}
}

func TestUnitPhaseInputsParseExposure(t *testing.T) {
	for _, s := range []string{"all", "read", "write"} {
		if e, err := phaseinputs.ParseExposure(s); err != nil || string(e) != s {
			t.Errorf("ParseExposure(%q) = %q, %v", s, e, err)
		}
	}
	if _, err := phaseinputs.ParseExposure("both"); !errors.Is(err, phaseinputs.ErrInputs) {
		t.Errorf("ParseExposure(both) = %v", err)
	}
}

func TestUnitPhaseInputsPhase(t *testing.T) {
	for _, tt := range []struct {
		state wire.WorkerState
		task  bool
		want  phaseinputs.Exposure
	}{
		{wire.WorkerStateINITIALIZING, false, phaseinputs.Read},
		{wire.WorkerStatePLANNING, false, phaseinputs.Read},
		{wire.WorkerStateAPPLYING, false, phaseinputs.Write},
		{wire.WorkerStateDESTROYING, false, phaseinputs.Write},
		{wire.WorkerStatePERFORMING, false, phaseinputs.Write},
		{wire.WorkerStateINITIALIZING, true, phaseinputs.Write},
		{wire.WorkerStatePERFORMING, true, phaseinputs.Write},
	} {
		if got := phaseinputs.Phase(tt.state, tt.task); got != tt.want {
			t.Errorf("Phase(%s, %v) = %s, want %s", tt.state, tt.task, got, tt.want)
		}
	}
	if !phaseinputs.All.Admits(phaseinputs.Read) || !phaseinputs.Read.Admits(phaseinputs.Read) ||
		phaseinputs.Read.Admits(phaseinputs.Write) || phaseinputs.Write.Admits(phaseinputs.Read) {
		t.Error("Admits")
	}
}

func TestUnitPhaseInputsCheckVariable(t *testing.T) {
	for _, tt := range []struct {
		configured, exposure, name string
		e                          phaseinputs.Exposure
	}{
		{"ro_TOKEN", "", "TOKEN", phaseinputs.Read},
		{"wo_TOKEN", "write", "TOKEN", phaseinputs.Write},
		{"TF_PLUGIN_CACHE_DIR", "all", "TF_PLUGIN_CACHE_DIR", phaseinputs.All},
	} {
		name, e, err := phaseinputs.CheckVariable(tt.configured, tt.exposure)
		if err != nil || name != tt.name || e != tt.e {
			t.Errorf("CheckVariable(%q, %q) = %q, %q, %v", tt.configured, tt.exposure, name, e, err)
		}
	}
	for _, tt := range []struct{ configured, exposure string }{
		{"ro_", ""},
		{"1TOKEN", ""},
		{"TO-KEN", ""},
		{"ro_TOKEN", "write"},
		{"TOKEN", "read"},
		{"INFRAWEAVE_API_TOKEN", ""},
		{"ro_TF_VAR_infraweave_run_state", ""},
		{"wo_TF_HTTP_PASSWORD", ""},
		{"SSL_CERT_FILE", ""},
	} {
		if _, _, err := phaseinputs.CheckVariable(tt.configured, tt.exposure); !errors.Is(err, phaseinputs.ErrInputs) {
			t.Errorf("CheckVariable(%q, %q) = %v, want refused", tt.configured, tt.exposure, err)
		}
	}
}

func TestUnitPhaseInputsCheckNames(t *testing.T) {
	if err := phaseinputs.CheckNames([]string{"ro_TOKEN", "wo_TOKEN", "OTHER"}); err != nil {
		t.Errorf("read and write of one name: %v", err)
	}
	for _, names := range [][]string{
		{"TOKEN", "ro_TOKEN"}, {"wo_TOKEN", "TOKEN"}, {"ro_TOKEN", "ro_TOKEN"}, {"A", "A"},
	} {
		if err := phaseinputs.CheckNames(names); !errors.Is(err, phaseinputs.ErrInputs) {
			t.Errorf("CheckNames(%q) = %v, want refused", names, err)
		}
	}
}

func TestUnitPhaseInputsCheckTargets(t *testing.T) {
	if err := phaseinputs.CheckTargets([]string{"/mnt/cache", "/etc/app.conf"},
		[]string{"/mnt/credentials/read.json", "/mnt/credentials/write.json", "/var/run/keys/key"}); err != nil {
		t.Errorf("valid targets: %v", err)
	}
	for _, tt := range []struct{ mounted, scoped []string }{
		{[]string{"relative"}, nil},
		{[]string{"/mnt/a/../b"}, nil},
		{[]string{"/mnt/a/"}, nil},
		{[]string{"/"}, nil},
		{[]string{"/mnt/workspace/x"}, nil},
		{[]string{"/usr/local/bin/tool"}, nil},
		{[]string{"/opt"}, nil},
		{[]string{"/mnt/a", "/mnt/a/b"}, nil},
		{[]string{"/mnt/a"}, []string{"/mnt/a"}},
		{nil, []string{"/top"}},
		{nil, []string{"/proc/self/x"}},
		{[]string{"/mnt/keys/file"}, []string{"/mnt/keys/other"}},
		{[]string{"/mnt"}, []string{"/mnt/keys/other"}},
		{nil, []string{"/opt/infraweave/x"}},
	} {
		if err := phaseinputs.CheckTargets(tt.mounted, tt.scoped); !errors.Is(err, phaseinputs.ErrInputs) {
			t.Errorf("CheckTargets(%q, %q) = %v, want refused", tt.mounted, tt.scoped, err)
		}
	}
}

func TestUnitPhaseInputsCheck(t *testing.T) {
	ok := phaseinputs.Inputs{
		Variables: []phaseinputs.Variable{
			{Name: "TOKEN", Exposure: phaseinputs.Read, Value: "r"},
			{Name: "TOKEN", Exposure: phaseinputs.Write, Value: "w", Sensitive: true},
			{Name: "CACHE", Exposure: phaseinputs.All, Value: "/c"},
		},
		Files: []phaseinputs.File{
			{Target: "/mnt/keys/read", Exposure: phaseinputs.Read, Content: []byte("r")},
			{Target: "/mnt/keys/write", Exposure: phaseinputs.Write, Content: []byte("w")},
		},
	}
	if err := phaseinputs.Check(ok); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got := phaseinputs.Dirs(ok.Files); !slices.Equal(got, []string{"/mnt/keys"}) {
		t.Errorf("Dirs = %q", got)
	}
	big := bytes.Repeat([]byte{'x'}, phaseinputs.MaxFile)
	many := make([]phaseinputs.File, 0, 17)
	for i := range 17 {
		many = append(many, phaseinputs.File{
			Target: "/mnt/keys/" + string(rune('a'+i)), Exposure: phaseinputs.Read, Content: big,
		})
	}
	for _, in := range []phaseinputs.Inputs{
		{Variables: []phaseinputs.Variable{{Name: "INFRAWEAVE_X", Exposure: phaseinputs.All}}},
		{Variables: []phaseinputs.Variable{{Name: "A", Exposure: phaseinputs.All}, {Name: "A", Exposure: phaseinputs.Read}}},
		{Files: []phaseinputs.File{{Target: "/mnt/keys/a", Exposure: phaseinputs.All}}},
		{Files: []phaseinputs.File{{Target: "/mnt/keys/a", Exposure: phaseinputs.Read, Content: append(big, 'x')}}},
		{Files: many},
		{Files: []phaseinputs.File{{Target: "/a", Exposure: phaseinputs.Read}}},
	} {
		if err := phaseinputs.Check(in); !errors.Is(err, phaseinputs.ErrInputs) {
			t.Errorf("Check(%+v) = %v, want refused", in.Variables, err)
		}
	}
}
