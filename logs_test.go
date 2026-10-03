package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
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
	got := nextLogsCommandAt("shop", "web", logsFlags{since: "2026-09-29T12:00:00Z", limit: 50, grep: "it's down"}, "2026-10-01T12:00:00.5Z", time.Now())
	want := `gg logs shop/web --since 2026-09-29T12:00:00Z --until 2026-10-01T12:00:00.5Z -n 50 --grep 'it'\''s down'`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if strings.Contains(nextLogsCommandAt("shop", "web", logsFlags{previous: true}, "x", time.Now()), "previous") {
		t.Error("the store pages; --previous is the node's and has no page before")
	}
}

// gg run prints what this run wrote. The store lags behind a job that has just
// finished, so asking it for "the latest lines" of a job that ran before
// answers with the earlier run's. The run's own start bounds the question, and
// a store with nothing since then is what sends the engine to the node.
func TestRunAsksOnlyForThisRunsLines(t *testing.T) {
	var got url.Values
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/logs") {
			got = r.URL.Query()
			_ = json.NewEncoder(w).Encode(map[string]any{"logs": "migrated\n", "source": "cluster"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"services": []any{map[string]any{
			"name": "migrate",
			"actual": map[string]any{"run": map[string]any{
				"revision": 7, "phase": "done", "exit_code": 0, "started_at": "2026-10-03T12:00:00.25Z",
			}},
		}}})
	})
	capture(t, func() {
		if err := waitForRun("shop", "migrate", 7); err != nil {
			t.Fatal(err)
		}
	})
	if got.Get("since") != "2026-10-03T12:00:00.25Z" {
		t.Errorf("asked %v; want since= the run's start", got)
	}
}

// A relative --since is worked out again every time the command runs, so the
// hint pins it: pasted back an hour later it still reads the same window, not
// one whose start has moved past --until.
func TestNextLogsCommandPinsARelativeSince(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	got := nextLogsCommandAt("shop", "web", logsFlags{since: "2h"}, "2026-10-03T11:30:00Z", now)
	want := `gg logs shop/web --since 2026-10-03T10:00:00Z --until 2026-10-03T11:30:00Z`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	got = nextLogsCommandAt("shop", "web", logsFlags{since: "2d"}, "2026-10-03T11:30:00Z", now)
	if !strings.Contains(got, "--since 2026-10-01T12:00:00Z") {
		t.Errorf("days: %s", got)
	}
}
