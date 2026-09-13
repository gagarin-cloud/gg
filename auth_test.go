package main

// Exit status, which is the part of onboarding nothing else checks.
//
// `gg` documents GAGARIN_TOKEN "for CI where no human can click a link", so a
// script running `gg login --claim` is an intended path — and there a zero exit
// on a failed claim means the script carries on believing it holds a
// credential. The next command fails as `unauthorized`, which sends whoever
// reads the log looking at permissions instead of at the claim that never
// completed.
//
// It was reported as broken on 2026-09-01 and does not reproduce; these pin it
// so that a later refactor of the command wiring cannot quietly make it true.
// The assertion is on the error rather than on os.Exit because main is one line
// — an error out of Execute is exit 1 — and testing that line would mean
// forking a process to learn what the compiler already guarantees.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAClaimThatFailsIsANonZeroExit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			// The claim is gone, expired, or was never minted. A human reads the
			// message; a script only ever sees the status.
			name: "the control plane refuses the code",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"code":"no_such_claim","message":"no claim \"ZZZZ-9999\""}}`))
			},
		},
		{
			// A 500 with no envelope: the failure gg cannot interpret must still
			// be a failure.
			name: "the control plane is broken",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			t.Setenv("GAGARIN_API", srv.URL)

			cmd := rootCmd()
			cmd.SetArgs([]string{"login", "--claim", "ZZZZ-9999"})
			if err := cmd.Execute(); err == nil {
				t.Fatal("a failed claim returned no error, so gg exits 0 and a script proceeds without a credential")
			}
		})
	}
}

// The one that was actually reported: a control plane that does not resolve.
// It is a different branch — the request never gets a response at all — and it
// is the branch a typo in GAGARIN_API lands on.
func TestAnUnreachableControlPlaneIsANonZeroExit(t *testing.T) {
	// Started and immediately closed, so the address is real and nothing is
	// listening. More faithful than an unresolvable name, which would depend on
	// what the test machine's resolver does with NXDOMAIN.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	t.Setenv("GAGARIN_API", url)

	cmd := rootCmd()
	cmd.SetArgs([]string{"login", "--claim", "ZZZZ-9999"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("an unreachable control plane returned no error, so gg exits 0")
	}
}

// The two halves are one command, so which half you meant is inferred from the
// argument. This pins that inference, because getting it wrong is silent: an
// address read as a code polls for a claim nobody minted, and a code read as an
// address mails an approval request to "ABCD-1234".
func TestLoginReadsABareArgumentForWhatItIs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		path string
	}{
		{"an address asks", []string{"login", "you@example.com"}, "/v1/signup"},
		{"a bare code collects", []string{"login", "ABCD-1234"}, "/v1/claim"},
		{"--claim collects", []string{"login", "--claim", "ABCD-1234"}, "/v1/claim"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			// Both halves are refused, so the command returns as soon as it has
			// shown which endpoint it believes it wants — which is the whole
			// assertion. A 200 on the claim would send this into docker login.
			fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
				got = r.URL.Path
				w.WriteHeader(http.StatusBadGateway)
			})

			cmd := rootCmd()
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err == nil {
				t.Fatal("a refusing control plane returned no error")
			}
			if got != tc.path {
				t.Fatalf("%v called %s, want %s", tc.args, got, tc.path)
			}
		})
	}
}

// Bare `gg login` on a machine with no credential is the error an agent is most
// likely to meet, so it teaches the flow rather than reporting a missing flag.
func TestBareLoginSaysWhatToDo(t *testing.T) {
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("bare `gg login` called %s; it should ask nothing of the API", r.URL.Path)
		w.WriteHeader(http.StatusBadGateway)
	})
	// fakeAPI sets GAGARIN_TOKEN, which is a credential by another route and
	// would take this down the "already acts as" branch.
	t.Setenv("GAGARIN_TOKEN", "")

	cmd := rootCmd()
	cmd.SetArgs([]string{"login"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("bare `gg login` succeeded on a machine with no credential")
	}
	if !strings.Contains(err.Error(), "gg login EMAIL") {
		t.Fatalf("the error does not say what to run: %v", err)
	}
}
