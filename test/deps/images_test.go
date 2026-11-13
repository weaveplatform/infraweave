package deps

import (
	"errors"
	"regexp"
	"testing"
)

// pinned is an image reference deps-refresh.yml can move: a release tag,
// GitLab's carrying its edition and Spacelift's a v and its rebuild's date,
// or a beta or release candidate of a major, and the digest of the image's
// multi-platform index.
var pinned = regexp.MustCompile(
	`^[a-z0-9./-]+:v?(\d+)(?:[0-9.]*(?:-ce\.0|_\d{8})?|(?:beta|rc)\d+)@sha256:[0-9a-f]{64}$`,
)

// TestUnitImagesPinned checks every image is pinned by digest, and that each
// PostgreSQL pin stays on the major its tests depend on: the floor, and the
// major below it that must be refused.
func TestUnitImagesPinned(t *testing.T) {
	for _, tc := range []struct {
		name, image, major string
	}{
		{"ImagePostgres", ImagePostgres, "18"},
		{"ImagePostgresNext", ImagePostgresNext, "19"},
		{"ImagePostgresBelowFloor", ImagePostgresBelowFloor, "17"},
		{"ImageObjectStore", ImageObjectStore, ""},
		{"ImageFakeGCS", ImageFakeGCS, ""},
		{"ImageAzurite", ImageAzurite, ""},
		{"ImageVault", ImageVault, ""},
		{"ImageMoto", ImageMoto, ""},
		{"ImageBitbucketDC", ImageBitbucketDC, ""},
		{"ImageGitLabCE", ImageGitLabCE, ""},
		{"ImageSpaceliftRunnerTerraform", ImageSpaceliftRunnerTerraform, ""},
	} {
		m := pinned.FindStringSubmatch(tc.image)
		switch {
		case m == nil:
			t.Errorf("%s = %q is not pinned by a release tag and digest", tc.name, tc.image)
		case tc.major != "" && m[1] != tc.major:
			t.Errorf("%s = %q, want PostgreSQL %s", tc.name, tc.image, tc.major)
		}
	}
}

func TestUnitPostgresImage(t *testing.T) {
	for env, want := range map[string]string{"": ImagePostgres, "next": ImagePostgresNext} {
		t.Setenv(PostgresEnv, env)
		if got, err := Postgres(); err != nil || got != want {
			t.Errorf("%s=%q: %s, %v", PostgresEnv, env, got, err)
		}
	}
	t.Setenv(PostgresEnv, "nxt")
	if _, err := Postgres(); !errors.Is(err, ErrPostgresEnv) {
		t.Error("a misspelt value chose an image")
	}
}
