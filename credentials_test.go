package main

// Minting a credential for CI.
//
// The behaviour these pin is mostly about *where output goes*, which is unusual
// for this codebase and is the point: the secret has to be pipeable into a
// secret store, and everything else has to stay out of the pipe.

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func mintServer(t *testing.T, resp string) *map[string]any {
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

const mintResp = `{"id":7,"credential":"gg_secret_value","account":"vik@example.com",
  "name":"github actions: acme/web","scopes":["deploy"],"expires_at":"2026-12-04T00:00:00Z"}`

// stdout carries the secret and nothing else, so that piping it into a secret
// store is a sane thing to do rather than something that needs a grep.
func TestTheSecretIsAloneOnStdout(t *testing.T) {
	mintServer(t, mintResp)
	out := capture(t, func() {
		if err := cmdCredentialsCreate("github actions: acme/web", 0); err != nil {
			t.Error(err)
		}
	})
	if strings.TrimSpace(out) != "gg_secret_value" {
		t.Errorf("stdout is not exactly the secret, so `--body \"$(gg creds create ...)\"` would carry prose:\n%q", out)
	}
}

// And the human-readable half is on stderr, where a pipe does not take it but a
// person still sees it.
func TestTheExplanationGoesToStderr(t *testing.T) {
	mintServer(t, mintResp)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prev := os.Stderr
	os.Stderr = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	_ = capture(t, func() {
		if err := cmdCredentialsCreate("ci", 0); err != nil {
			t.Error(err)
		}
	})
	w.Close()
	os.Stderr = prev
	errOut := <-done

	for _, want := range []string{"only time it is shown", "GAGARIN_TOKEN", "cannot destroy"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr is missing %q:\n%s", want, errOut)
		}
	}
	// The id, because revoking is the recovery for losing the secret and the id
	// is the only thing you need for it.
	if !strings.Contains(errOut, "revoke 7") {
		t.Errorf("stderr does not say how to revoke it:\n%s", errOut)
	}
}

// Refused locally, before a request. The name is what somebody reads months
// later deciding whether a credential is still wanted.
func TestCreatingWithoutANameIsRefused(t *testing.T) {
	for _, name := range []string{"", "   "} {
		err := cmdCredentialsCreate(name, 0)
		if err == nil {
			t.Fatalf("a nameless credential was accepted for %q", name)
		}
		if !strings.Contains(err.Error(), "--name") {
			t.Errorf("the refusal does not name the flag: %v", err)
		}
	}
}

// Absent means the platform's default rather than zero, which would be an
// expiry in the past.
func TestExpiryIsOnlySentWhenAsked(t *testing.T) {
	body := mintServer(t, mintResp)
	_ = capture(t, func() {
		if err := cmdCredentialsCreate("ci", 0); err != nil {
			t.Error(err)
		}
	})
	if _, present := (*body)["expires_in_days"]; present {
		t.Errorf("an unasked-for expiry was sent: %#v", *body)
	}

	body = mintServer(t, mintResp)
	_ = capture(t, func() {
		if err := cmdCredentialsCreate("ci", 30); err != nil {
			t.Error(err)
		}
	})
	if (*body)["expires_in_days"] != float64(30) {
		t.Errorf("expires did not reach the request: %#v", *body)
	}
}

// The listing has to mark the credential you are holding, because the first
// thing somebody does with this list is look for the one they must not revoke.
func TestTheListingMarksTheCurrentCredential(t *testing.T) {
	mintServer(t, `{"credentials":[
	  {"id":3,"client":"Claude Code on mbp","scopes":["deploy"],"current":true,"minted":false},
	  {"id":7,"client":"github actions","scopes":["deploy"],"current":false,"minted":true}]}`)
	out := capture(t, func() {
		if err := cmdCredentialsList(); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "*  3") {
		t.Errorf("the current credential is not marked:\n%s", out)
	}
	// And which are CI tokens, because an unused laptop credential and an unused
	// CI token deserve different suspicion.
	if !strings.Contains(out, "minted") || !strings.Contains(out, "approved") {
		t.Errorf("the listing does not distinguish how a credential came to exist:\n%s", out)
	}
}

// A credential that has never been used is a different fact from one used at
// the zero time, and only one of them is worth printing.
func TestNeverUsedReadsAsNever(t *testing.T) {
	mintServer(t, `{"credentials":[
	  {"id":5,"client":"gg on build-box","scopes":["deploy"],"current":false,"minted":false}]}`)
	out := capture(t, func() {
		if err := cmdCredentialsList(); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "never") {
		t.Errorf("a never-used credential does not say so:\n%s", out)
	}
}

// The environment must not be able to name the client. GITHUB_ACTIONS used to
// return a bare institutional string with no host in it, which is what a human
// would read in an approval email.
func TestClientNameIsNotForgeableAsCI(t *testing.T) {
	// Cleared so the assertion is about GITHUB_ACTIONS and not about whichever
	// harness happens to be running the test suite.
	for _, k := range []string{"CLAUDECODE", "CLAUDE_CODE", "CURSOR_AGENT", "AIDER"} {
		t.Setenv(k, "")
	}
	t.Setenv("GITHUB_ACTIONS", "true")

	got := clientName()
	if got == "GitHub Actions" {
		t.Error("the environment can still make gg claim to be CI in an approval email")
	}
	if !strings.Contains(got, "gg on ") {
		t.Errorf("clientName = %q, want the ordinary host-bearing name", got)
	}
}

// The agent names that remain are still only hints, and the reason they are
// tolerable is the hostname: a human reading an approval email recognises the
// machine even when the first half is wrong.
func TestAgentNamesStillCarryAHost(t *testing.T) {
	for _, k := range []string{"CLAUDE_CODE", "CURSOR_AGENT", "AIDER", "GITHUB_ACTIONS"} {
		t.Setenv(k, "")
	}
	t.Setenv("CLAUDECODE", "1")
	got := clientName()
	if !strings.HasPrefix(got, "Claude Code on ") || len(got) <= len("Claude Code on ") {
		t.Errorf("clientName = %q, want a name ending in a hostname", got)
	}
}
