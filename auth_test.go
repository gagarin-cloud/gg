package main

// `gg login` against a control plane that speaks the device grant.
//
// The server here implements the two endpoints the way brain/docs/051 in the
// gagarin repo describes them, and each test scripts what the token endpoint
// answers on each poll. What is asserted is what a script or an agent can see:
// the exit (an error or not), the code in the error, and whether a credential
// file exists afterwards.
//
// Exit status matters as much as it did before: a script that runs `gg login`
// and gets a zero exit on a refusal carries on believing it holds a credential,
// and the next command fails as `unauthorized`, which sends whoever reads the
// log looking at permissions instead of at the sign-in that never completed.
//
// These tests wait in real time, because the waiting is in oauth2's polling loop
// and the interval is the server's to set. The smallest the protocol allows is
// one second, and slow_down costs five more.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// deviceServer is a control plane that answers the device grant. tokenReplies
// are the RFC 6749 error codes the token endpoint gives on successive polls; an
// empty string means "issue the credential". Past the end, the last one repeats.
type deviceServer struct {
	t            *testing.T
	tokenReplies []string

	mu        sync.Mutex
	authForm  map[string]string
	polls     []time.Time
	pollForms []map[string]string
}

func (d *deviceServer) handler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		d.t.Errorf("%s: %v", r.URL.Path, err)
	}
	form := map[string]string{}
	for k := range r.PostForm {
		form[k] = r.PostForm.Get(k)
	}
	w.Header().Set("Content-Type", "application/json")

	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case r.Method == "POST" && r.URL.Path == "/oauth/device_authorization":
		d.authForm = form
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":               "ggd_test",
			"user_code":                 "BCDF-GHJK",
			"verification_uri":          "https://api.example/device",
			"verification_uri_complete": "https://api.example/device?user_code=BCDF-GHJK",
			"expires_in":                900,
			"interval":                  1,
		})
	case r.Method == "POST" && r.URL.Path == "/oauth/token":
		n := len(d.polls)
		d.polls = append(d.polls, time.Now())
		d.pollForms = append(d.pollForms, form)
		reply := ""
		if len(d.tokenReplies) > 0 {
			reply = d.tokenReplies[min(n, len(d.tokenReplies)-1)]
		}
		w.Header().Set("Cache-Control", "no-store")
		if reply != "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": reply})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "gg_issued",
			"token_type":   "Bearer",
			"expires_in":   7776000,
			"scope":        "deploy",
		})
	case r.Method == "GET" && r.URL.Path == "/v1/whoami":
		if got := r.Header.Get("Authorization"); got != "Bearer gg_issued" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"no"}}`))
			return
		}
		// No registry, so the docker step fails before it ever runs docker.
		_, _ = w.Write([]byte(`{"account":"you@example.com","client":"gg on test","can":["deploy"]}`))
	default:
		d.t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

// startLogin points gg at a device server with a clean credential directory and
// no credential from the environment, so the file is the only place one can
// land.
func startLogin(t *testing.T, replies ...string) *deviceServer {
	t.Helper()
	d := &deviceServer{t: t, tokenReplies: replies}
	fakeAPI(t, d.handler)
	t.Setenv("GAGARIN_TOKEN", "")
	t.Setenv("GAGARIN_REGISTRY", "")
	return d
}

func runLogin(t *testing.T) (string, error) {
	t.Helper()
	var err error
	out := capture(t, func() {
		cmd := rootCmd()
		cmd.SetArgs([]string{"login"})
		err = cmd.Execute()
	})
	return out, err
}

func assertNoCredentialFile(t *testing.T) {
	t.Helper()
	path, _ := credentialsPath()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a failed sign-in left a credential file at %s", path)
	}
}

func TestLoginWaitsForApprovalThenStoresTheCredential(t *testing.T) {
	d := startLogin(t, "authorization_pending", "authorization_pending", "")

	out, err := runLogin(t)
	if err != nil {
		t.Fatalf("gg login failed: %v\n%s", err, out)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.polls) != 3 {
		t.Errorf("polled %d times, want 3 (pending, pending, token)", len(d.polls))
	}
	for k, want := range map[string]string{"client_id": "gg", "scope": "deploy", "label": clientName()} {
		if d.authForm[k] != want {
			t.Errorf("device_authorization %s = %q, want %q", k, d.authForm[k], want)
		}
	}
	last := d.pollForms[len(d.pollForms)-1]
	for k, want := range map[string]string{
		"client_id":   "gg",
		"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
		"device_code": "ggd_test",
	} {
		if last[k] != want {
			t.Errorf("token %s = %q, want %q", k, last[k], want)
		}
	}

	// What the human is told has to be in the output before the wait, and whole.
	for _, want := range []string{
		"https://api.example/device?user_code=BCDF-GHJK",
		"https://api.example/device",
		"BCDF-GHJK",
		"GitHub or Google",
		"this machine now acts as you@example.com",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "user_code=BCDF-GHJK") > strings.Index(out, "waiting") {
		t.Errorf("the link comes after the wait starts:\n%s", out)
	}

	creds, err := loadCredentials()
	if err != nil {
		t.Fatalf("no credential file after approval: %v", err)
	}
	if creds.Credential != "gg_issued" || creds.Account != "you@example.com" {
		t.Errorf("stored %+v", creds)
	}
	if creds.API != os.Getenv("GAGARIN_API") {
		t.Errorf("stored api %q, want %q", creds.API, os.Getenv("GAGARIN_API"))
	}
	if len(creds.Scopes) != 1 || creds.Scopes[0] != "deploy" {
		t.Errorf("stored scopes %v, want [deploy]", creds.Scopes)
	}
}

// slow_down is the control plane's to send and gg's to obey: every later poll
// waits five seconds longer. A client that ignores it gets itself rate limited.
func TestLoginSlowsDownWhenTold(t *testing.T) {
	d := startLogin(t, "slow_down", "")

	if out, err := runLogin(t); err != nil {
		t.Fatalf("gg login failed: %v\n%s", err, out)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.polls) != 2 {
		t.Fatalf("polled %d times, want 2", len(d.polls))
	}
	if gap := d.polls[1].Sub(d.polls[0]); gap < 5500*time.Millisecond {
		t.Errorf("polled again %v after slow_down, want at least six seconds", gap)
	}
}

func TestLoginRefusalsAreNonZeroExits(t *testing.T) {
	for _, tc := range []struct {
		reply string
		hint  string
	}{
		{"access_denied", "ask them"},
		{"expired_token", "gg login again"},
	} {
		t.Run(tc.reply, func(t *testing.T) {
			startLogin(t, tc.reply)

			out, err := runLogin(t)
			if err == nil {
				t.Fatalf("%s returned no error, so gg exits 0 and a script proceeds without a credential\n%s", tc.reply, out)
			}
			if !strings.Contains(err.Error(), "["+tc.reply+"]") || !strings.Contains(err.Error(), "hint: ") ||
				!strings.Contains(err.Error(), tc.hint) {
				t.Errorf("error is not in gg's shape with a hint: %v", err)
			}
			assertNoCredentialFile(t)
		})
	}
}

// A control plane that does not answer at all is the branch a typo in
// GAGARIN_API lands on.
func TestLoginAgainstAnUnreachableControlPlaneIsANonZeroExit(t *testing.T) {
	// Started and immediately closed, so the address is real and nothing is
	// listening. More faithful than an unresolvable name, which would depend on
	// what the test machine's resolver does with NXDOMAIN.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	t.Setenv("GAGARIN_API", url)
	t.Setenv("GAGARIN_TOKEN", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	_, err := runLogin(t)
	if err == nil {
		t.Fatal("an unreachable control plane returned no error, so gg exits 0")
	}
	if !strings.Contains(err.Error(), "cannot reach control plane") {
		t.Errorf("error does not say the control plane is unreachable: %v", err)
	}
	assertNoCredentialFile(t)
}

// The email and --claim forms are gone, and saying so beats polling for
// something that will never be approved.
func TestLoginTakesNoArguments(t *testing.T) {
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("gg login with an argument called %s", r.URL.Path)
	})
	for _, args := range [][]string{{"login", "you@example.com"}, {"login", "--claim", "ABCD-1234"}} {
		cmd := rootCmd()
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
}
