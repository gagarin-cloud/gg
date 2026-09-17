package main

// `gg login` against a control plane that speaks the device grant.
//
// The server here implements the two endpoints the way brain/docs/051 in the
// gagarin repo describes them, and each test scripts what the token endpoint
// answers on each poll. What is asserted is what a script or an agent can see:
// the exit (an error or not), the code in the error, the text, and which files
// exist afterwards.
//
// Exit status matters as much as it did before: a script that runs `gg login`
// and gets a zero exit on a refusal carries on believing it holds a credential,
// and the next command fails as `unauthorized`, which sends whoever reads the
// log looking at permissions instead of at the sign-in that never completed.
//
// The polling itself is oauth2's, on a real clock, and the smallest interval the
// protocol allows is one second — so a test that polls costs a second or two.
// resumeWait is cut to match; the tests that need no poll need no time.

import (
	"encoding/json"
	"fmt"
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
	t *testing.T

	mu           sync.Mutex
	tokenReplies []string
	authForms    []map[string]string
	polls        []time.Time
	pollForms    []map[string]string
}

func (d *deviceServer) reply(replies ...string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.tokenReplies = replies
}

func (d *deviceServer) counts() (asks, polls int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.authForms), len(d.polls)
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
		d.authForms = append(d.authForms, form)
		n := len(d.authForms)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":               fmt.Sprintf("ggd_%d", n),
			"user_code":                 fmt.Sprintf("BCDF-GHJ%d", n),
			"verification_uri":          "https://api.example/device",
			"verification_uri_complete": fmt.Sprintf("https://api.example/device?user_code=BCDF-GHJ%d", n),
			"expires_in":                900,
			"interval":                  1,
		})
	case r.Method == "POST" && r.URL.Path == "/oauth/token":
		n := len(d.polls)
		d.polls = append(d.polls, time.Now())
		d.pollForms = append(d.pollForms, form)
		reply := "authorization_pending"
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

// startLogin points gg at a device server with a clean credential directory, no
// credential from the environment, and an agent's stdout — not a terminal.
func startLogin(t *testing.T, replies ...string) *deviceServer {
	t.Helper()
	d := &deviceServer{t: t, tokenReplies: replies}
	fakeAPI(t, d.handler)
	t.Setenv("GAGARIN_TOKEN", "")
	t.Setenv("GAGARIN_REGISTRY", "")
	setTerminal(t, false)
	setResumeWait(t, 1500*time.Millisecond)
	return d
}

func setTerminal(t *testing.T, tty bool) {
	prev := stdoutIsTerminal
	stdoutIsTerminal = func() bool { return tty }
	t.Cleanup(func() { stdoutIsTerminal = prev })
}

func setResumeWait(t *testing.T, d time.Duration) {
	prev := resumeWait
	resumeWait = d
	t.Cleanup(func() { resumeWait = prev })
}

func runLogin(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var err error
	out := capture(t, func() {
		cmd := rootCmd()
		cmd.SetArgs(append([]string{"login"}, args...))
		err = cmd.Execute()
	})
	return out, err
}

func pendingExists(t *testing.T) bool {
	t.Helper()
	path, _ := pendingLoginPath()
	_, err := os.Stat(path)
	return err == nil
}

func assertNoCredentialFile(t *testing.T) {
	t.Helper()
	path, _ := credentialsPath()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a failed sign-in left a credential file at %s", path)
	}
}

// An agent's first run prints what to relay and hands control straight back, so
// the agent can speak before anybody has approved anything.
func TestLoginForAnAgentAsksAndExits(t *testing.T) {
	d := startLogin(t)

	out, err := runLogin(t)
	if err != nil {
		t.Fatalf("gg login failed: %v\n%s", err, out)
	}
	asks, polls := d.counts()
	if asks != 1 || polls != 0 {
		t.Errorf("asked %d times and polled %d, want 1 and 0", asks, polls)
	}
	d.mu.Lock()
	for k, want := range map[string]string{"client_id": "gg", "scope": "deploy", "label": clientName()} {
		if d.authForms[0][k] != want {
			t.Errorf("device_authorization %s = %q, want %q", k, d.authForms[0][k], want)
		}
	}
	d.mu.Unlock()

	for _, want := range []string{
		"https://api.example/device?user_code=BCDF-GHJ1",
		"go to https://api.example/device and enter the code BCDF-GHJ1",
		"GitHub or Google",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if last := lines[len(lines)-1]; last != "when your human has approved, run: gg login" {
		t.Errorf("last line is %q, want the instruction to run gg login again", last)
	}

	path, _ := pendingLoginPath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("no pending file: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("pending file mode %o, want 600", mode)
	}
	p := loadPendingLogin()
	if p == nil || p.DeviceCode != "ggd_1" || p.UserCode != "BCDF-GHJ1" || p.API != os.Getenv("GAGARIN_API") ||
		p.Interval != 1 || p.Label != clientName() || p.ExpiresAt.Before(time.Now().Add(14*time.Minute)) {
		t.Errorf("pending file holds %+v", p)
	}
	assertNoCredentialFile(t)
}

// The second run collects what the first asked for, rather than asking again.
func TestLoginForAnAgentCollectsOnceApproved(t *testing.T) {
	d := startLogin(t)
	if out, err := runLogin(t); err != nil {
		t.Fatalf("first run: %v\n%s", err, out)
	}

	d.reply("")
	out, err := runLogin(t)
	if err != nil {
		t.Fatalf("second run: %v\n%s", err, out)
	}
	asks, polls := d.counts()
	if asks != 1 || polls != 1 {
		t.Errorf("asked %d times and polled %d, want 1 and 1", asks, polls)
	}
	d.mu.Lock()
	for k, want := range map[string]string{
		"client_id":   "gg",
		"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
		"device_code": "ggd_1",
	} {
		if d.pollForms[0][k] != want {
			t.Errorf("token %s = %q, want %q", k, d.pollForms[0][k], want)
		}
	}
	d.mu.Unlock()
	if !strings.Contains(out, "this machine now acts as you@example.com") {
		t.Errorf("output does not name the account:\n%s", out)
	}

	creds, err := loadCredentials()
	if err != nil {
		t.Fatalf("no credential file after approval: %v", err)
	}
	if creds.Credential != "gg_issued" || creds.Account != "you@example.com" ||
		creds.API != os.Getenv("GAGARIN_API") || len(creds.Scopes) != 1 || creds.Scopes[0] != "deploy" {
		t.Errorf("stored %+v", creds)
	}
	if pendingExists(t) {
		t.Error("the pending file survived a collected sign-in")
	}
}

// Nobody has approved yet: say so, non-zero, with the link again so the agent
// can pass it on again — and keep the request, because it is still good.
func TestLoginForAnAgentStillPendingIsANonZeroExit(t *testing.T) {
	startLogin(t)
	if out, err := runLogin(t); err != nil {
		t.Fatalf("first run: %v\n%s", err, out)
	}

	out, err := runLogin(t)
	if err == nil {
		t.Fatalf("an unapproved sign-in exited 0\n%s", out)
	}
	if !strings.Contains(err.Error(), "[authorization_pending]") || !strings.Contains(err.Error(), "hint: ") {
		t.Errorf("error is not in gg's shape with a hint: %v", err)
	}
	if !strings.Contains(out, "https://api.example/device?user_code=BCDF-GHJ1") {
		t.Errorf("the link is not printed again:\n%s", out)
	}
	if !pendingExists(t) {
		t.Error("a still-good request was thrown away")
	}
	assertNoCredentialFile(t)
}

// slow_down is the control plane's to send and gg's to obey: every later poll
// waits five seconds longer. Within a 2.5 second wait on a one-second interval,
// a client that ignored it would poll twice.
func TestLoginSlowsDownWhenTold(t *testing.T) {
	d := startLogin(t)
	if out, err := runLogin(t); err != nil {
		t.Fatalf("first run: %v\n%s", err, out)
	}
	d.reply("slow_down")
	setResumeWait(t, 2500*time.Millisecond)

	if _, err := runLogin(t); err == nil || !strings.Contains(err.Error(), "[authorization_pending]") {
		t.Fatalf("want authorization_pending, got %v", err)
	}
	if _, polls := d.counts(); polls != 1 {
		t.Errorf("polled %d times after slow_down, want 1", polls)
	}
}

func TestLoginRefusalsThrowTheRequestAway(t *testing.T) {
	for _, tc := range []struct {
		reply string
		hint  string
	}{
		{"access_denied", "ask them"},
		{"expired_token", "gg login again"},
	} {
		t.Run(tc.reply, func(t *testing.T) {
			d := startLogin(t)
			if out, err := runLogin(t); err != nil {
				t.Fatalf("first run: %v\n%s", err, out)
			}
			d.reply(tc.reply)

			out, err := runLogin(t)
			if err == nil {
				t.Fatalf("%s returned no error, so gg exits 0 and a script proceeds without a credential\n%s", tc.reply, out)
			}
			if !strings.Contains(err.Error(), "["+tc.reply+"]") || !strings.Contains(err.Error(), "hint: ") ||
				!strings.Contains(err.Error(), tc.hint) {
				t.Errorf("error is not in gg's shape with a hint: %v", err)
			}
			if pendingExists(t) {
				t.Errorf("the pending file survived %s", tc.reply)
			}
			assertNoCredentialFile(t)
		})
	}
}

func TestLoginNewDiscardsTheRequest(t *testing.T) {
	d := startLogin(t)
	if out, err := runLogin(t); err != nil {
		t.Fatalf("first run: %v\n%s", err, out)
	}

	out, err := runLogin(t, "--new")
	if err != nil {
		t.Fatalf("gg login --new: %v\n%s", err, out)
	}
	asks, polls := d.counts()
	if asks != 2 || polls != 0 {
		t.Errorf("asked %d times and polled %d, want 2 and 0", asks, polls)
	}
	if p := loadPendingLogin(); p == nil || p.DeviceCode != "ggd_2" {
		t.Errorf("pending file holds %+v, want the new request", p)
	}
	if !strings.Contains(out, "BCDF-GHJ2") {
		t.Errorf("the new code is not printed:\n%s", out)
	}
}

// A request that expired while nobody was looking cannot be collected, so the
// next run asks afresh instead of failing on it.
func TestLoginAsksAgainWhenTheRequestExpired(t *testing.T) {
	d := startLogin(t)
	if err := savePendingLogin(&pendingLogin{
		API: os.Getenv("GAGARIN_API"), DeviceCode: "ggd_old", UserCode: "OLDC-ODE0",
		VerificationURI: "https://api.example/device", Interval: 1,
		ExpiresAt: time.Now().Add(-time.Minute), Label: "gg on test",
	}); err != nil {
		t.Fatal(err)
	}

	out, err := runLogin(t)
	if err != nil {
		t.Fatalf("gg login: %v\n%s", err, out)
	}
	if asks, polls := d.counts(); asks != 1 || polls != 0 {
		t.Errorf("asked %d times and polled %d, want 1 and 0", asks, polls)
	}
	if !strings.Contains(out, "expired") || !strings.Contains(out, "BCDF-GHJ1") {
		t.Errorf("output does not say the old code expired and give a new one:\n%s", out)
	}
}

// A person at a terminal is reading along, so one run does the whole thing.
func TestLoginAtATerminalWaits(t *testing.T) {
	d := startLogin(t, "")
	setTerminal(t, true)

	out, err := runLogin(t)
	if err != nil {
		t.Fatalf("gg login failed: %v\n%s", err, out)
	}
	if asks, polls := d.counts(); asks != 1 || polls != 1 {
		t.Errorf("asked %d times and polled %d, want 1 and 1", asks, polls)
	}
	if strings.Index(out, "user_code=BCDF-GHJ1") > strings.Index(out, "waiting for approval") {
		t.Errorf("the link comes after the wait starts:\n%s", out)
	}
	if strings.Contains(out, "run: gg login") {
		t.Errorf("a terminal run told the reader to run gg login again:\n%s", out)
	}
	if _, err := loadCredentials(); err != nil {
		t.Errorf("no credential file: %v", err)
	}
	if pendingExists(t) {
		t.Error("the pending file survived a collected sign-in")
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
	setTerminal(t, false)

	_, err := runLogin(t)
	if err == nil {
		t.Fatal("an unreachable control plane returned no error, so gg exits 0")
	}
	if !strings.Contains(err.Error(), "cannot reach control plane") {
		t.Errorf("error does not say the control plane is unreachable: %v", err)
	}
	if pendingExists(t) {
		t.Error("a request that was never made left a pending file")
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
