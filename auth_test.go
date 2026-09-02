package main

// Exit status, which is the part of onboarding nothing else checks.
//
// `gg` documents GAGARIN_TOKEN "for CI where no human can click a link", so a
// script running `gg auth --claim` is an intended path — and there a zero exit
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
			cmd.SetArgs([]string{"auth", "--claim", "ZZZZ-9999"})
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
	cmd.SetArgs([]string{"auth", "--claim", "ZZZZ-9999"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("an unreachable control plane returned no error, so gg exits 0")
	}
}
