package api

import (
	"net/http"
	"testing"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func TestUnitChangedETag(t *testing.T) {
	for _, tt := range []struct {
		name, after, kept string
		wantError         bool
	}{
		{name: "changed", after: `"2"`, kept: "before"},
		{name: "unchanged", after: `"1"`, kept: "before", wantError: true},
		{name: "missing response tag", kept: "before", wantError: true},
		{name: "unknown kept tag", after: `"2"`, kept: "unknown", wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			u := updateSteps{
				w: &harness.World{Sent: []harness.Response{{
					Header: http.Header{"Etag": []string{tt.after}},
				}}},
				etags: map[string]string{"before": `"1"`},
			}
			if err := u.changed(tt.kept); (err != nil) != tt.wantError {
				t.Fatalf("changed(%q) = %v, want error %v", tt.kept, err, tt.wantError)
			}
		})
	}
}
