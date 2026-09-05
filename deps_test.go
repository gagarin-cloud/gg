package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The dependency graph and the credentials that ride on it, at the wire.
//
// What these pin is the part that misleads rather than merely breaks. The whole
// point of `--deps` is that a service needing a database can be declared and
// deployed in one call; if the field silently stopped reaching the body, the
// deploy would still succeed, the pod would still start, and it would hang on
// its first query with nothing in the output to say why.

// captureBody runs f against a server that records the last request body it was
// given, and hands both back. The handler answers everything a deploy or a
// `gg deps` asks on the way through.
func captureBody(t *testing.T, f func() error) (map[string]any, error) {
	t.Helper()
	var last map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b, err := io.ReadAll(r.Body); err == nil && len(b) > 0 {
			_ = json.Unmarshal(b, &last)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/needs"):
			// The shape the endpoint answers with since injection landed:
			// the set, the variables it bought, and the sentence.
			_, _ = w.Write([]byte(`{"service":"api","needs":["db"],
				"injected":["DB_DATABASE","DB_HOST","DB_PASSWORD","DB_PORT","DB_URL","DB_USER"],
				"sentence":"api reaches db, and nothing else."}`))
		case strings.HasSuffix(r.URL.Path, "/status"):
			_, _ = w.Write([]byte(`{"project":"shop","services":[{"name":"api","needs":[]}]}`))
		default:
			_, _ = w.Write([]byte(`{"name":"api"}`))
		}
	}))
	defer srv.Close()
	t.Setenv("GAGARIN_API", srv.URL)
	err := f()
	return last, err
}

// target is a registryTarget with a digest already in it, so deployImage does
// not reach for `docker buildx imagetools inspect` — the digest lookup is not
// what these tests are about, and shelling out to a registry would make them
// depend on a daemon.
func target() *registryTarget {
	return &registryTarget{
		project: "shop", projectID: "p1", repo: "api", tag: "v3",
		ref:    "reg.example/p1/api:v3",
		digest: "sha256:" + strings.Repeat("a", 64),
	}
}

func TestDeploySendsDeps(t *testing.T) {
	body, err := captureBody(t, func() error {
		return deployImage("shop", "api", 8080, target(), &deployFlags{
			env: map[string]string{}, deps: []string{"db"},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := body["deps"].([]any)
	if !ok {
		t.Fatalf("no deps in the deploy body, so --deps is silently dropped: %#v", body)
	}
	if len(got) != 1 || got[0] != "db" {
		t.Errorf("deps = %#v, want [db]", got)
	}
}

// Absent means "change nothing". The field has to be missing rather than an
// empty array: the server unions what arrives, so `[]` would be a no-op, but it
// would still record a graph write against a deploy that never mentioned the
// graph.
func TestDeployWithoutDepsOmitsTheField(t *testing.T) {
	body, err := captureBody(t, func() error {
		return deployImage("shop", "api", 8080, target(), &deployFlags{env: map[string]string{}})
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, present := body["deps"]; present {
		t.Errorf("a deploy with no --deps sent deps anyway: %#v", body["deps"])
	}
}

// `gg deps add` is the call that hands a service its credentials, so it has to
// say which ones arrived.
func TestDepsAddPrintsTheInjectedNames(t *testing.T) {
	var out string
	_, err := captureBody(t, func() error {
		var e error
		out = capture(t, func() { e = cmdDepsAdd("shop/api", []string{"db"}) })
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"DB_URL", "DB_PASSWORD", "DB_DATABASE"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing from `gg deps add` output, so a service was handed\ncredentials without being told which:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "api reaches db") {
		t.Errorf("the sentence went missing:\n%s", out)
	}
}

// Names only. The values are one call away at `gg resource secrets`, and a
// password printed by a command that asked about a graph ends up in every
// scrollback and agent transcript that ever connected a database.
//
// Asserted as shape rather than by hunting for known secrets: a variable name
// legitimately contains the word PASSWORD, and the output legitimately names
// `gg resource secrets`. What a leak would look like is a KEY=VALUE pair or a
// connection URL, so those are what this refuses.
func TestDepsAddPrintsNoValues(t *testing.T) {
	var out string
	_, err := captureBody(t, func() error {
		var e error
		out = capture(t, func() { e = cmdDepsAdd("shop/api", []string{"db"}) })
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "=") {
		t.Errorf("`gg deps add` printed a KEY=VALUE pair, which is a credential in a\nterminal that asked about a graph:\n%s", out)
	}
	for _, scheme := range []string{"://"} {
		if strings.Contains(out, scheme) {
			t.Errorf("`gg deps add` printed something shaped like a connection URL:\n%s", out)
		}
	}
}

// A withdrawal reports the remainder, not the delta: `gg deps rm` takes
// variables away as surely as `add` grants them.
func TestDepsSendsTheUnionNotJustTheAddition(t *testing.T) {
	var last map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b, err := io.ReadAll(r.Body); err == nil && len(b) > 0 {
			_ = json.Unmarshal(b, &last)
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/status") {
			// It already reaches cache. Adding db must not drop it.
			_, _ = w.Write([]byte(`{"project":"shop","services":[{"name":"api","needs":["cache"]}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"needs":["cache","db"],"sentence":"api reaches cache and db, and nothing else."}`))
	}))
	defer srv.Close()
	t.Setenv("GAGARIN_API", srv.URL)

	if out := capture(t, func() {
		if err := cmdDepsAdd("shop/api", []string{"db"}); err != nil {
			t.Error(err)
		}
	}); out == "" {
		t.Error("no output")
	}

	needs, ok := last["needs"].([]any)
	if !ok || len(needs) != 2 {
		t.Fatalf("needs = %#v, want [cache db]: adding one dependency must not withdraw another", last["needs"])
	}
}
