package main

// Isolating tests from the machine running them.
//
// This file exists because of a failure that only CI could produce. Every test
// that stood up an httptest server set GAGARIN_API and stopped there, which
// passes on a developer's laptop for the wrong reason: resolveAuth falls back to
// ~/.config/gagarin/credentials.json, finds a real one, and the request goes out
// authenticated with a live credential. On a clean checkout there is no such
// file, so seventeen tests failed at once with "this machine has no gagarin
// credentials".
//
// Two things were wrong with that, and only one of them was the red build. A
// test suite that reads the developer's own credential file is a test suite
// whose result depends on who ran it — and one careless test away from sending a
// real request to a real control plane with a real credential.

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeAPI points gg at a server the test controls, and cuts every other route
// to a credential.
//
// GAGARIN_TOKEN is set because resolveAuth prefers it over the file, so the
// file is never consulted. XDG_CONFIG_HOME is redirected as well, so that a
// path which reads or *writes* the credential file lands in a temporary
// directory rather than in the home directory of whoever is running the tests.
// Belt and braces on purpose: the first is what makes the tests pass, the
// second is what stops a future test quietly saving over somebody's login.
func fakeAPI(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	t.Setenv("GAGARIN_API", srv.URL)
	t.Setenv("GAGARIN_TOKEN", "gg_test_token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return srv
}
