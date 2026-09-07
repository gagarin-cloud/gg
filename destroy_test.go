package main

// Which endpoint a name is destroyed through.
//
// gg asks the platform what a name is and calls the endpoint for it, so the
// caller never has to know. That lookup read "anything that is not a container
// is a resource", which was true until jobs existed — after which `gg destroy
// shop/probe` on a job called the resource endpoint and came back refused, and
// the promise in the help text ("you do not have to say which") was false for
// one of the three things a name can be.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// destroyRoutes runs cmdDestroy against a project holding one row of the given
// kind, and reports the path the DELETE went to alongside what was printed.
func destroyRoutes(t *testing.T, kind string) (path, printed string) {
	t.Helper()
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/status") {
			_ = json.NewEncoder(w).Encode(statusResp{
				Project:  "demo",
				Services: []serviceStatus{{Name: "probe", Kind: kind}},
			})
			return
		}
		if r.Method == http.MethodDelete {
			path = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
	})

	printed = capture(t, func() {
		if err := cmdDestroy("demo/probe"); err != nil {
			t.Errorf("destroy: %v", err)
		}
	})
	return path, printed
}

func TestDestroyPicksTheEndpointForTheKind(t *testing.T) {
	for _, tc := range []struct {
		kind, path, says string
	}{
		{"job", "/v1/projects/demo/services/probe", "job probe destroyed"},
		{"container", "/v1/projects/demo/services/probe", "service probe destroyed"},
		{"", "/v1/projects/demo/services/probe", "service probe destroyed"},
		{"resource:postgres", "/v1/projects/demo/resources/probe", "resource probe destroyed"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			path, printed := destroyRoutes(t, tc.kind)
			if path != tc.path {
				t.Errorf("deleted through %s, want %s", path, tc.path)
			}
			if !strings.Contains(printed, tc.says) {
				t.Errorf("printed %q, want it to contain %q", printed, tc.says)
			}
		})
	}
}
