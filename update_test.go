package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// atATerminal makes the run look like a person at a shell, outside CI, with
// no opt-out set: the one situation the notice is for.
func atATerminal(t *testing.T) {
	t.Helper()
	prev := stderrIsTerminal
	stderrIsTerminal = func() bool { return true }
	t.Cleanup(func() { stderrIsTerminal = prev })
	t.Setenv("CI", "")
	t.Setenv("GAGARIN_NO_UPDATE_CHECK", "")
}

func TestUpdateNoticeSaysWhenBehind(t *testing.T) {
	atATerminal(t)
	asVersion(t, "0.53.0")
	serveRelease(t, "v0.54.0", nil)

	var out bytes.Buffer
	startUpdateNotice([]string{"status", "shop"})(&out)
	if want := "gg v0.54.0 is available (you have v0.53.0). Upgrade: gg upgrade\n"; out.String() != want {
		t.Fatalf("notice = %q, want %q", out.String(), want)
	}
}

func TestUpdateNoticeIsQuietWhenCurrent(t *testing.T) {
	atATerminal(t)
	asVersion(t, "0.54.0")
	serveRelease(t, "v0.54.0", nil)

	var out bytes.Buffer
	startUpdateNotice([]string{"status"})(&out)
	if out.Len() != 0 {
		t.Fatalf("notice = %q; want nothing at the latest release", out.String())
	}
}

// Whoever is not a person at a terminal never hears it, and the network is
// not asked on their behalf.
func TestUpdateNoticeGating(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(t *testing.T)
		args  []string
	}{
		{"not a terminal", func(t *testing.T) { stderrIsTerminal = func() bool { return false } }, []string{"status"}},
		{"CI", func(t *testing.T) { t.Setenv("CI", "true") }, []string{"status"}},
		{"opted out", func(t *testing.T) { t.Setenv("GAGARIN_NO_UPDATE_CHECK", "1") }, []string{"status"}},
		{"checkout build", func(t *testing.T) { asVersion(t, "dev") }, []string{"status"}},
		{"shell completion", func(t *testing.T) {}, []string{"__complete", "st"}},
		{"completion script", func(t *testing.T) {}, []string{"completion", "zsh"}},
		{"upgrade", func(t *testing.T) {}, []string{"upgrade"}},
		{"version", func(t *testing.T) {}, []string{"version"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			atATerminal(t)
			asVersion(t, "0.53.0")
			r := serveRelease(t, "v0.54.0", nil)
			c.setup(t)

			var out bytes.Buffer
			startUpdateNotice(c.args)(&out)
			if out.Len() != 0 {
				t.Fatalf("notice = %q; want silence", out.String())
			}
			if n := r.hits.Load(); n != 0 {
				t.Fatalf("made %d requests; want none", n)
			}
		})
	}
}

func TestUpdateNoticeAsksAtMostOnceADay(t *testing.T) {
	atATerminal(t)
	asVersion(t, "0.53.0")
	r := serveRelease(t, "v0.54.0", nil)

	for i := 0; i < 3; i++ {
		var out bytes.Buffer
		startUpdateNotice([]string{"status"})(&out)
		if !strings.Contains(out.String(), "v0.54.0 is available") {
			t.Fatalf("run %d: notice = %q", i, out.String())
		}
	}
	// HEAD /latest and the redirect's target is two requests, once.
	if n := r.hits.Load(); n > 2 {
		t.Fatalf("made %d requests over three runs; want one check", n)
	}

	writeUpdateCheck(updateCheck{CheckedAt: time.Now().Add(-25 * time.Hour), Latest: "v0.54.0"})
	before := r.hits.Load()
	startUpdateNotice([]string{"status"})(&bytes.Buffer{})
	if r.hits.Load() == before {
		t.Fatal("a day-old check was not repeated")
	}
}

// A check that cannot finish costs at most updateCheckTimeout, says nothing,
// and is not retried on the very next command.
func TestUpdateNoticeGivesUpQuietly(t *testing.T) {
	atATerminal(t)
	asVersion(t, "0.53.0")
	block := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-block }))
	t.Cleanup(func() { close(block); slow.Close() })
	t.Setenv("GAGARIN_RELEASES", slow.URL)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	prev := updateCheckTimeout
	updateCheckTimeout = 100 * time.Millisecond
	t.Cleanup(func() { updateCheckTimeout = prev })

	start := time.Now()
	var out bytes.Buffer
	startUpdateNotice([]string{"status"})(&out)
	if d := time.Since(start); d > time.Second {
		t.Fatalf("the notice held the command up for %v", d)
	}
	if out.Len() != 0 {
		t.Fatalf("notice = %q; want nothing when the check failed", out.String())
	}

	// The failed attempt is remembered once the check gives up.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if c, ok := readUpdateCheck(); ok && time.Since(c.CheckedAt) < time.Minute {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a failed check was not recorded, so the next command would try again")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestUpdateNoticeUnreachableNeverFails(t *testing.T) {
	atATerminal(t)
	asVersion(t, "0.53.0")
	t.Setenv("GAGARIN_RELEASES", "http://127.0.0.1:1")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var out bytes.Buffer
	startUpdateNotice([]string{"status"})(&out)
	if out.Len() != 0 {
		t.Fatalf("notice = %q; want nothing when releases are unreachable", out.String())
	}
}
