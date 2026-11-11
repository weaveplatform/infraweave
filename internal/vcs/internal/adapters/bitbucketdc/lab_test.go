package bitbucketdc_test

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/bitbucketdc"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/sourcetest"
	"github.com/weaveplatform/infraweave/internal/vcs/vcslab"
)

// TestAccBitbucketDCLab runs the source contract against the vcs-lab
// job's repository on its Bitbucket Data Center container, and checks
// that a run's repository access token reads that repository and not
// another the integration reaches, and nothing once revoked.
func TestAccBitbucketDCLab(t *testing.T) {
	lab := sourcetest.LabEnv(t, vcslab.EnvHost, vcslab.EnvToken, vcslab.EnvNamespace,
		vcslab.EnvRepository, vcslab.EnvOther, vcslab.EnvCAFile)
	pem, err := os.ReadFile(lab.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatalf("%s holds no certificate", lab.CAFile)
	}
	in := integrationOf(lab.Host, lab.Token)
	dc := bitbucketdc.New(bitbucketdc.Options{RootCAs: roots})
	repo := port.Repository{Namespace: lab.Namespace, Name: lab.Repository, Integration: &in}
	gone := repo
	gone.Name += "-gone"
	sourcetest.Run(t, dc, sourcetest.Lab(repo, gone))

	c, err := dc.Mint(t.Context(), repo, "infraweave-run-lab", time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
	}}
	clone := func(name string) string {
		return strings.TrimSuffix(lab.Host, "/") + "/scm/" + strings.ToLower(lab.Namespace) +
			"/" + name + ".git"
	}
	sourcetest.Scope(t, client, c, clone(lab.Repository), clone(lab.Other), false)
	if err := dc.Revoke(t.Context(), repo, c.ID); err != nil {
		t.Fatal(err)
	}
	if ok, err := sourcetest.Reads(
		t.Context(),
		client,
		clone(lab.Repository),
		c,
	); ok ||
		err != nil {
		t.Errorf("a revoked token reads = %t, %v", ok, err)
	}
}
