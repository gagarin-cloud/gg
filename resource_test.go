package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- external resources ----------------------------------------------------
//
// The type gg has to know by name, because two things it does locally depend on
// it: --env is refused for anything else before a request is made, and the
// output after a create says what was published rather than that something is
// being provisioned.

func TestExternalSendsItsEnv(t *testing.T) {
	var out string
	body, err := captureBody(t, func() error {
		var e error
		out = capture(t, func() {
			e = cmdResourceAdd("shop/openai", "external", "", 0,
				map[string]string{"API_KEY": "sk-secret", "BASE_URL": "https://api.openai.com/"})
		})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if body["type"] != "external" {
		t.Errorf("type = %v", body["type"])
	}
	env, ok := body["env"].(map[string]any)
	if !ok {
		t.Fatalf("no env in the request, so an external would publish nothing: %#v", body)
	}
	if env["API_KEY"] != "sk-secret" {
		t.Errorf("env = %#v", env)
	}
	// The caller typed the un-prefixed halves, so the output has to show what an
	// application will actually read — the prefix rule is not guessable.
	for _, want := range []string{"OPENAI_API_KEY", "OPENAI_BASE_URL"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing from the output:\n%s", want, out)
		}
	}
	// Names, never values. These are live credentials and the terminal keeps a
	// scrollback.
	if strings.Contains(out, "sk-secret") {
		t.Errorf("a live key was echoed back into the terminal:\n%s", out)
	}
	// Nothing is provisioned, so nothing is being waited for.
	if strings.Contains(out, "provisioning") {
		t.Errorf("an external does not provision anything:\n%s", out)
	}
	// And the asymmetry is said where somebody will read it.
	if !strings.Contains(out, "egress") {
		t.Errorf("the output lets a reader assume the declaration restricts egress:\n%s", out)
	}
}

// A dashed name is a legal DNS label and an illegal shell variable, so the hint
// has to show the translated prefix or it names a variable that will not exist.
func TestExternalOutputTranslatesADashedName(t *testing.T) {
	var out string
	if _, err := captureBody(t, func() error {
		var e error
		out = capture(t, func() {
			e = cmdResourceAdd("shop/openai-eu", "external", "", 0, map[string]string{"API_KEY": "x"})
		})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "OPENAI_EU_API_KEY") {
		t.Errorf("the prefix was not translated:\n%s", out)
	}
}

// Refused locally, before a request. A caller who could set a postgres's
// environment could set POSTGRES_PASSWORD to a string the running database has
// never heard of — the control plane refuses it too, but saying so here costs
// no round trip and names the command that does what they wanted.
func TestEnvIsRefusedOnAMintedType(t *testing.T) {
	for _, typ := range []string{"postgres", "valkey", "ferretdb"} {
		err := cmdResourceAdd("shop/db", typ, "", 0, map[string]string{"API_KEY": "x"})
		if err == nil {
			t.Fatalf("%s accepted --env", typ)
		}
		if !strings.Contains(err.Error(), "resource secrets") {
			t.Errorf("%s: the refusal does not name how to read them instead: %v", typ, err)
		}
	}
}

// An external with nothing in it publishes nothing, which is a row somebody
// created by mistake rather than a thing they wanted.
func TestExternalWithNoValuesIsRefused(t *testing.T) {
	err := cmdResourceAdd("shop/openai", "external", "", 0, nil)
	if err == nil {
		t.Fatal("an external with no values was accepted")
	}
	if !strings.Contains(err.Error(), "--env-file") {
		t.Errorf("the hint does not point at the flag to use: %v", err)
	}
}

// --- rotation --------------------------------------------------------------

// rotateServer answers a rotate the way the engine does, and records the body
// it was sent.
func rotateServer(t *testing.T, resp string) *map[string]any {
	t.Helper()
	var last map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b, err := io.ReadAll(r.Body); err == nil && len(b) > 0 {
			_ = json.Unmarshal(b, &last)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("GAGARIN_API", srv.URL)
	return &last
}

// A database's new password is the platform's to mint, so the request carries
// nothing. Sending an empty env would be a caller choosing a password.
func TestRotatingADatabaseSendsNoEnv(t *testing.T) {
	body := rotateServer(t, `{"resource":"db","type":"postgres","rotated":["DB_URL"],"dependents":["api"],
	  "sentence":"db has new credentials, and api is restarting to pick them up."}`)
	out := capture(t, func() {
		if err := cmdResourceRotate("shop/db", nil); err != nil {
			t.Error(err)
		}
	})
	if _, present := (*body)["env"]; present {
		t.Errorf("a database rotation sent env: %#v", *body)
	}
	if !strings.Contains(out, "DB_URL") {
		t.Errorf("the output does not name what was rotated:\n%s", out)
	}
	if !strings.Contains(out, "api is restarting") {
		t.Errorf("the output does not say what was rolled:\n%s", out)
	}
}

// An external's values are the caller's, so they go on the wire.
func TestRotatingAnExternalSendsItsEnv(t *testing.T) {
	body := rotateServer(t, `{"resource":"openai","type":"external","rotated":["OPENAI_API_KEY"],
	  "dependents":["bot"],"sentence":"openai is publishing the new values, and bot is restarting to pick them up."}`)
	out := capture(t, func() {
		if err := cmdResourceRotate("shop/openai", map[string]string{"API_KEY": "sk-two"}); err != nil {
			t.Error(err)
		}
	})
	env, ok := (*body)["env"].(map[string]any)
	if !ok || env["API_KEY"] != "sk-two" {
		t.Fatalf("the new values did not reach the request: %#v", *body)
	}
	// Names, never values. This command runs because a credential changed, so
	// printing one puts the replacement in the scrollback that replaced it.
	if strings.Contains(out, "sk-two") {
		t.Errorf("the new credential was echoed into the terminal:\n%s", out)
	}
}

// A valkey restart empties it. That is what a restart of the type always does,
// but it is worth being told rather than discovering it from a cold cache.
func TestRotatingAValkeySaysTheCacheWasEmptied(t *testing.T) {
	rotateServer(t, `{"resource":"cache","type":"valkey","rotated":["CACHE_URL"],"dependents":["api"],
	  "restarted":true,"sentence":"cache has new credentials, and api is restarting to pick them up."}`)
	out := capture(t, func() {
		if err := cmdResourceRotate("shop/cache", nil); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "held in memory is gone") {
		t.Errorf("a cache was emptied without saying so:\n%s", out)
	}
}

// A postgres takes an ALTER ROLE live, so there is no restart to warn about and
// warning anyway would be a lie about an outage.
func TestRotatingAPostgresDoesNotClaimARestart(t *testing.T) {
	rotateServer(t, `{"resource":"db","type":"postgres","rotated":["DB_URL"],"dependents":["api"],
	  "restarted":false,"sentence":"db has new credentials, and api is restarting to pick them up."}`)
	out := capture(t, func() {
		if err := cmdResourceRotate("shop/db", nil); err != nil {
			t.Error(err)
		}
	})
	if strings.Contains(out, "held in memory is gone") {
		t.Errorf("a database rotation claimed an outage it did not cause:\n%s", out)
	}
}

// A rotation nothing was holding is not a failure, but silence would read as
// one — the caller expected services to restart and none did.
func TestRotatingWithNoDependentsSaysSo(t *testing.T) {
	rotateServer(t, `{"resource":"stripe","type":"external","rotated":["STRIPE_SECRET_KEY"],
	  "dependents":[],"sentence":"stripe is publishing the new values, and nothing declares it yet."}`)
	out := capture(t, func() {
		if err := cmdResourceRotate("shop/stripe", map[string]string{"SECRET_KEY": "x"}); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "Nothing declares stripe yet") {
		t.Errorf("no explanation for why nothing restarted:\n%s", out)
	}
	if !strings.Contains(out, "gg deps add") {
		t.Errorf("the output does not say how to connect something:\n%s", out)
	}
}
