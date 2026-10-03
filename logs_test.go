package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The flags reach the route as the parameters it reads, and nothing is sent
// that was not given: the defaults are the server's.
func TestLogsSendsOnlyTheFlagsGiven(t *testing.T) {
	var got url.Values
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		_ = json.NewEncoder(w).Encode(map[string]any{"logs": "a\n", "source": "store", "next": nil})
	})

	capture(t, func() {
		if err := cmdLogs("shop/web", logsFlags{}); err != nil {
			t.Fatal(err)
		}
	})
	if len(got) != 0 {
		t.Errorf("no flags sent %v", got)
	}

	capture(t, func() {
		if err := cmdLogs("shop/web", logsFlags{since: "2h", until: "30m", limit: 50, grep: "panic: x", previous: true}); err != nil {
			t.Fatal(err)
		}
	})
	want := url.Values{"since": {"2h"}, "until": {"30m"}, "limit": {"50"}, "q": {"panic: x"}, "previous": {"true"}}
	if got.Encode() != want.Encode() {
		t.Errorf("sent %v, want %v", got, want)
	}
}

// stdout is the lines and only the lines; the notice and the next page go to
// stderr, where a pipe does not see them.
func TestLogsKeepsStdoutForTheLines(t *testing.T) {
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"logs": "one\ntwo\n", "source": "cluster",
			"next": "2026-10-01T12:00:00.5Z", "notice": "the log store did not answer",
		})
	})
	out := capture(t, func() {
		if err := cmdLogs("shop/web", logsFlags{}); err != nil {
			t.Fatal(err)
		}
	})
	if out != "one\ntwo\n" {
		t.Errorf("stdout = %q", out)
	}
}

func TestNextLogsCommandCanBePastedBack(t *testing.T) {
	got := nextLogsCommand("shop", "web", logsFlags{since: "2d", limit: 50, grep: "it's down"}, "2026-10-01T12:00:00.5Z")
	want := `gg logs shop/web --since 2d --until 2026-10-01T12:00:00.5Z -n 50 --grep 'it'\''s down'`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if strings.Contains(nextLogsCommand("shop", "web", logsFlags{previous: true}, "x"), "previous") {
		t.Error("the store pages; --previous is the node's and has no page before")
	}
}
