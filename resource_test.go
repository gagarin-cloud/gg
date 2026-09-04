package main

import (
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
