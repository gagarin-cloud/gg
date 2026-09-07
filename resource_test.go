package main

import (
	"encoding/json"
	"io"
	"net/http"
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
	for _, typ := range []string{"postgres", "valkey", "qdrant"} {
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

// --- rolling an external back ----------------------------------------------
//
// The undo config never had. A service rollback cannot do this — the injected
// half is re-derived from the resources as they are now, deliberately, so that
// nobody is ever put back onto a rotated password — which leaves the resource
// itself as the only place a config change can be undone.

// The output is worded for a row that runs nothing. "Revision 3 is what is
// running now" would be false of an external, and false in the direction that
// sends somebody looking for a pod.
func TestRollingBackAnExternalPrintsWhatMoved(t *testing.T) {
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"revision":3,"restored_from":1,
		  "service":{"kind":"resource:external"},
		  "changed":["CFG_LOG_LEVEL"],"removed":["CFG_REGION"],"dependents":["web","worker"],
		  "sentence":"cfg is publishing the values from revision 1 again, recorded as revision 3, and web and worker are restarting to pick them up."}`))
	})
	out := capture(t, func() {
		if err := cmdRollback("shop/cfg", 1); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "Changed: CFG_LOG_LEVEL") {
		t.Errorf("the output does not name what moved:\n%s", out)
	}
	if !strings.Contains(out, "No longer published: CFG_REGION") {
		t.Errorf("the output does not name what a rollback took away:\n%s", out)
	}
	if !strings.Contains(out, "publishes now") {
		t.Errorf("the output should not talk about what is running for a row that runs nothing:\n%s", out)
	}
	if strings.Contains(out, "is what is running now") {
		t.Errorf("an external does not run, and this line says it does:\n%s", out)
	}
}

// A service keeps the wording it had. The external branch must not swallow the
// ordinary case, which is the whole of what this command was for until now.
func TestRollingBackAServiceKeepsItsWording(t *testing.T) {
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"revision":9,"restored_from":7,"service":{"kind":"container"},
		  "sentence":"web is back to revision 7, recorded as revision 9."}`))
	})
	out := capture(t, func() {
		if err := cmdRollback("shop/web", 7); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "Revision 9 is what is running now") {
		t.Errorf("a service rollback lost its wording:\n%s", out)
	}
}

// --- rotation --------------------------------------------------------------

// rotateServer answers a rotate the way the engine does, and records the body
// it was sent.
func rotateServer(t *testing.T, resp string) *map[string]any {
	t.Helper()
	var last map[string]any
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if b, err := io.ReadAll(r.Body); err == nil && len(b) > 0 {
			_ = json.Unmarshal(b, &last)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	})
	return &last
}

// A database's new password is the platform's to mint, so the request carries
// nothing. Sending an empty env would be a caller choosing a password.
func TestRotatingADatabaseSendsNoEnv(t *testing.T) {
	body := rotateServer(t, `{"resource":"db","type":"postgres","rotated":["DB_URL"],"dependents":["api"],
	  "sentence":"db has new credentials, and api is restarting to pick them up."}`)
	out := capture(t, func() {
		if err := cmdResourceRotate("shop/db", nil, nil, nil); err != nil {
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
		if err := cmdResourceRotate("shop/openai", map[string]string{"API_KEY": "sk-two"}, nil, nil); err != nil {
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

// Changing one key of several is a different request from replacing the bundle,
// and it has to reach the wire as one: `env` here would take the other keys
// away from every dependent.
func TestAmendingAnExternalSendsSetNotEnv(t *testing.T) {
	body := rotateServer(t, `{"resource":"openai","type":"external",
	  "rotated":["OPENAI_API_KEY","OPENAI_BASE_URL"],"changed":["OPENAI_API_KEY"],"removed":[],
	  "dependents":["bot"],"sentence":"openai is publishing the new values, and bot is restarting to pick them up."}`)
	out := capture(t, func() {
		if err := cmdResourceRotate("shop/openai", nil, []string{"API_KEY=sk-two"}, nil); err != nil {
			t.Error(err)
		}
	})
	if _, present := (*body)["env"]; present {
		t.Errorf("an amendment sent env, which would drop the other keys: %#v", *body)
	}
	set, ok := (*body)["set"].(map[string]any)
	if !ok || set["API_KEY"] != "sk-two" {
		t.Fatalf("the new value did not reach the request: %#v", *body)
	}
	// Which of the published names actually moved, because the caller changed
	// one of two and should not have to work out which from the first line.
	if !strings.Contains(out, "Changed: OPENAI_API_KEY") {
		t.Errorf("the output does not say which key changed:\n%s", out)
	}
	if strings.Contains(out, "sk-two") {
		t.Errorf("the new credential was echoed into the terminal:\n%s", out)
	}
}

// A value containing an = is a value, not a second separator. Cut on the first
// one, as an .env file does — a base64 secret ends in padding and would
// otherwise arrive truncated, which is a failure that looks like a bad key.
func TestSetSplitsOnTheFirstEqualsOnly(t *testing.T) {
	body := rotateServer(t, `{"resource":"openai","type":"external","rotated":["OPENAI_API_KEY"],"dependents":[]}`)
	capture(t, func() {
		if err := cmdResourceRotate("shop/openai", nil, []string{"API_KEY=a=b=="}, nil); err != nil {
			t.Error(err)
		}
	})
	set, _ := (*body)["set"].(map[string]any)
	if set["API_KEY"] != "a=b==" {
		t.Errorf("the value was cut short: %#v", *body)
	}
}

func TestUnsetReachesTheWire(t *testing.T) {
	body := rotateServer(t, `{"resource":"openai","type":"external","rotated":["OPENAI_API_KEY"],
	  "changed":[],"removed":["OPENAI_BASE_URL"],"dependents":["bot"],
	  "sentence":"openai no longer publishes OPENAI_BASE_URL, and bot is restarting to pick them up."}`)
	out := capture(t, func() {
		if err := cmdResourceRotate("shop/openai", nil, nil, []string{"BASE_URL"}); err != nil {
			t.Error(err)
		}
	})
	unset, ok := (*body)["unset"].([]any)
	if !ok || len(unset) != 1 || unset[0] != "BASE_URL" {
		t.Fatalf("the removal did not reach the request: %#v", *body)
	}
	if !strings.Contains(out, "No longer published: OPENAI_BASE_URL") {
		t.Errorf("the output does not name what was taken away:\n%s", out)
	}
}

// The silent half of --env, made visible. Naming fewer keys than the resource
// held has just taken the others away from every dependent, and the alternative
// to this line is an application that can no longer authenticate.
func TestAReplacementSaysWhatItDropped(t *testing.T) {
	rotateServer(t, `{"resource":"openai","type":"external","rotated":["OPENAI_API_KEY"],
	  "changed":["OPENAI_API_KEY"],"removed":["OPENAI_BASE_URL"],"dependents":["bot"],
	  "sentence":"openai is publishing the new values, and bot is restarting to pick them up."}`)
	out := capture(t, func() {
		if err := cmdResourceRotate("shop/openai", map[string]string{"API_KEY": "sk-two"}, nil, nil); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "No longer published: OPENAI_BASE_URL") {
		t.Errorf("a replacement dropped a key without saying so:\n%s", out)
	}
	if !strings.Contains(out, "no longer has them") {
		t.Errorf("the output does not say what that costs:\n%s", out)
	}
}

// Refused locally rather than at the far end. There is an ordering that would
// give the combination a meaning, and it is not one anybody would predict from
// the command they typed.
func TestReplacingAndAmendingTogetherIsRefusedBeforeTheRequest(t *testing.T) {
	body := rotateServer(t, `{}`)
	err := cmdResourceRotate("shop/openai", map[string]string{"API_KEY": "sk-two"}, []string{"BASE_URL=x"}, nil)
	if err == nil {
		t.Fatal("combining a replacement with an amendment was accepted")
	}
	if !strings.Contains(err.Error(), "--set") {
		t.Errorf("the refusal does not name the flags in play: %v", err)
	}
	if *body != nil {
		t.Errorf("a round trip was spent on a request gg could refuse itself: %#v", *body)
	}
}

// K=V or nothing, and the message says which flag: a --set with no = is most
// likely somebody who meant --unset.
func TestSetWithoutAValueIsRefused(t *testing.T) {
	rotateServer(t, `{}`)
	err := cmdResourceRotate("shop/openai", nil, []string{"API_KEY"}, nil)
	if err == nil || !strings.Contains(err.Error(), "--set expects K=V") {
		t.Errorf("a --set with no value was not refused clearly: %v", err)
	}
}

// --unset takes a key. A K=V there is somebody who just typed --set and repeated
// the shape, and the round trip would come back refusing a key called
// "API_KEY=sk-two" — which reads as a platform that mangled the name.
func TestUnsetWithAValueIsRefusedLocally(t *testing.T) {
	body := rotateServer(t, `{}`)
	err := cmdResourceRotate("shop/openai", nil, nil, []string{"API_KEY=sk-two"})
	if err == nil || !strings.Contains(err.Error(), "--unset takes a key") {
		t.Fatalf("a --unset with a value was not refused clearly: %v", err)
	}
	if !strings.Contains(err.Error(), "--set API_KEY=sk-two") {
		t.Errorf("the hint does not offer the flag they probably meant: %v", err)
	}
	if *body != nil {
		t.Errorf("a round trip was spent on a request gg could refuse itself: %#v", *body)
	}
}

// A valkey restart empties it. That is what a restart of the type always does,
// but it is worth being told rather than discovering it from a cold cache.
//
// The words are the platform's and gg prints them verbatim, so what this asserts
// is that the note is not dropped on the floor — which is what the assertion
// below it exists to distinguish from.
func TestRotatingAValkeySaysTheCacheWasEmptied(t *testing.T) {
	rotateServer(t, `{"resource":"cache","type":"valkey","rotated":["CACHE_URL"],"dependents":["api"],
	  "restarted":true,"restart_note":"cache was restarted to read its new credential, so anything it held in memory is gone.",
	  "sentence":"cache has new credentials, and api is restarting to pick them up."}`)
	out := capture(t, func() {
		if err := cmdResourceRotate("shop/cache", nil, nil, nil); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "held in memory is gone") {
		t.Errorf("a cache was emptied without saying so:\n%s", out)
	}
}

// The other restarting type, and the reason the sentence is no longer gg's to
// compose. A qdrant is replaced on rotation too, but its data is on a volume —
// printing the cache's warning here would be the CLI reporting data loss that
// did not happen, which it did until 2026-09-06.
func TestRotatingAQdrantDoesNotClaimDataLoss(t *testing.T) {
	rotateServer(t, `{"resource":"vectors","type":"qdrant","rotated":["VECTORS_API_KEY"],"dependents":["api"],
	  "restarted":true,"restart_note":"vectors was restarted to read its new credential. Its data is on a volume, so nothing was lost.",
	  "sentence":"vectors has new credentials, and api is restarting to pick them up."}`)
	out := capture(t, func() {
		if err := cmdResourceRotate("shop/vectors", nil, nil, nil); err != nil {
			t.Error(err)
		}
	})
	if strings.Contains(out, "held in memory is gone") {
		t.Errorf("a qdrant rotation claimed data loss that did not happen:\n%s", out)
	}
	if !strings.Contains(out, "nothing was lost") {
		t.Errorf("the platform's restart note was not printed:\n%s", out)
	}
}

// A postgres takes an ALTER ROLE live, so there is no restart to warn about and
// warning anyway would be a lie about an outage.
func TestRotatingAPostgresDoesNotClaimARestart(t *testing.T) {
	rotateServer(t, `{"resource":"db","type":"postgres","rotated":["DB_URL"],"dependents":["api"],
	  "restarted":false,"sentence":"db has new credentials, and api is restarting to pick them up."}`)
	out := capture(t, func() {
		if err := cmdResourceRotate("shop/db", nil, nil, nil); err != nil {
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
		if err := cmdResourceRotate("shop/stripe", map[string]string{"SECRET_KEY": "x"}, nil, nil); err != nil {
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
