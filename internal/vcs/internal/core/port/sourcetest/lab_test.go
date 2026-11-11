package sourcetest

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/vcslab"
)

// TestUnitLabEnv checks that LabEnv skips a test whose lab is not
// configured and returns the lab that is.
func TestUnitLabEnv(t *testing.T) {
	t.Setenv(vcslab.EnvHost, "")
	t.Run("unset", func(t *testing.T) {
		defer func() {
			if !t.Skipped() {
				t.Error("a test with an unset lab ran")
			}
		}()
		LabEnv(t, vcslab.EnvHost)
	})
	t.Setenv(vcslab.EnvHost, "https://gitlab.example")
	if lab := LabEnv(t, vcslab.EnvHost); lab.Host != "https://gitlab.example" {
		t.Errorf("LabEnv = %+v", lab)
	}
}

func TestUnitLab(t *testing.T) {
	r := Lab(port.Repository{Name: "infra"}, port.Repository{Name: "gone"})
	if r.Repo.Name != "infra" || r.Missing.Name != "gone" || r.Head != vcslab.Head ||
		r.Base != vcslab.Base || r.Tagged != vcslab.Tagged || string(r.Content) != vcslab.Content ||
		!slices.Equal(r.Changed, vcslab.Changed()) {
		t.Errorf("Lab = %+v", r)
	}
}

// TestUnitReads checks that a ref advertisement reads, that a refusal of
// any kind, a sign-in page among them, does not, and that a server error,
// no answer and a URL that does not parse are errors; and that Scope
// holds a credential to what it should read.
func TestUnitReads(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/{repo}/info/refs", func(w http.ResponseWriter, req *http.Request) {
		user, token, _ := req.BasicAuth()
		switch {
		case req.URL.Query().Get("service") != "git-upload-pack":
			w.WriteHeader(http.StatusBadRequest)
		case chi.URLParam(req, "repo") == "broken":
			w.WriteHeader(http.StatusBadGateway)
		case user != "x-token" || token != "own" && token != "wide":
			w.WriteHeader(http.StatusUnauthorized)
		case chi.URLParam(req, "repo") == "signin":
			w.Header().Set("Content-Type", "text/html")
		case chi.URLParam(req, "repo") == "other" && token != "wide":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.Header().Set("Content-Type", advertisement)
		}
	})
	srv := httptest.NewServer(r)
	defer srv.Close()
	own := port.Credential{Username: "x-token", Token: []byte("own")}
	for name, c := range map[string]struct {
		url  string
		cred port.Credential
		want bool
	}{
		"its own":     {srv.URL + "/infra/", own, true},
		"another":     {srv.URL + "/other", own, false},
		"a bad token": {srv.URL + "/infra", port.Credential{Username: "x-token"}, false},
		"a sign-in":   {srv.URL + "/signin", own, false},
	} {
		if got, err := Reads(
			t.Context(),
			srv.Client(),
			c.url,
			c.cred,
		); err != nil ||
			got != c.want {
			t.Errorf("%s = %t, %v; want %t", name, got, err, c.want)
		}
	}
	if _, err := Reads(
		t.Context(),
		srv.Client(),
		srv.URL+"/broken",
		own,
	); !errors.Is(
		err,
		ErrHost,
	) {
		t.Errorf("a server error = %v", err)
	}
	for name, url := range map[string]string{
		"no answer": "http://127.0.0.1:1/infra",
		"no URL":    "http://[::1/infra",
	} {
		if _, err := Reads(t.Context(), srv.Client(), url, own); err == nil {
			t.Errorf("%s read", name)
		}
	}

	Scope(t, srv.Client(), own, srv.URL+"/infra", srv.URL+"/other", false)
	Scope(t, srv.Client(), port.Credential{Username: "x-token", Token: []byte("wide")},
		srv.URL+"/infra", srv.URL+"/other", true)
}
