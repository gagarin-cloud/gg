package main

// What `gg alerts` sends, and what it tells somebody to do next. Alerts are an
// opt-in with no body: the old --server/--topic/--token are gone, and the
// engine refuses a body that carries one. A device can only be added from a
// browser, so the output has to say that — loudly when there are none.

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAlertsOnOptsInWithNoBody(t *testing.T) {
	var method, path string
	var body []byte
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		body, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"enabled":true,"devices":2}`))
	})

	out := capture(t, func() {
		if err := cmdAlertsOn("shop"); err != nil {
			t.Fatal(err)
		}
	})
	if method != http.MethodPut || path != "/v1/projects/shop/alerts" {
		t.Errorf("unexpected request: %s %s", method, path)
	}
	if len(body) != 0 {
		t.Errorf("opting in carries nothing, sent %q", body)
	}
	for _, want := range []string{"alerts are on for shop", "notifications from the console",
		"https://my.gagarin.cloud/projects/shop/alerts", "gg alerts test shop"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "NO DEVICE YET") {
		t.Errorf("a member with devices needs no warning:\n%s", out)
	}
}

func TestAlertsOnWithNoDevicesSaysSoProminently(t *testing.T) {
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"enabled":true,"devices":0}`))
	})
	out := capture(t, func() {
		if err := cmdAlertsOn("shop"); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "NO DEVICE YET") || !strings.Contains(out, "https://my.gagarin.cloud/projects/shop/alerts") {
		t.Errorf("opting in with no device delivers nothing, and must say so:\n%s", out)
	}
}

// The ntfy flags are removed outright: a script that still passes one has to
// fail, not be quietly ignored.
func TestAlertsOnTakesNoNtfyFlags(t *testing.T) {
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a refused command called %s", r.URL.Path)
	})
	for _, flag := range []string{"--server", "--topic", "--token"} {
		cmd := rootCmd()
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"alerts", "on", "shop", flag, "x"})
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("%s: want an unknown flag error, got %v", flag, err)
		}
	}
}

func TestAlertsShowOffSaysHowToTurnThemOn(t *testing.T) {
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/notifications") {
			_, _ = w.Write([]byte(`{"notifications":[],"next":null}`))
			return
		}
		_, _ = w.Write([]byte(`{"enabled":false,"devices":1}`))
	})
	out := capture(t, func() {
		if err := cmdAlertsShow("shop"); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"alerts are off for shop", "gg alerts on shop", "no recent notifications"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

func TestAlertsShowListsStateDevicesAndFeed(t *testing.T) {
	var seen []string
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.RequestURI())
		if strings.HasSuffix(r.URL.Path, "/notifications") {
			_, _ = w.Write([]byte(`{"notifications":[
				{"id":"2","title":"web is back","body":"","url":"/projects/shop/services/web","resolved":true,"created_at":"2026-10-02T09:05:00Z"},
				{"id":"1","title":"web is down","body":"","url":"/projects/shop/services/web","resolved":false,"created_at":"2026-10-02T09:00:00Z"}],"next":null}`))
			return
		}
		_, _ = w.Write([]byte(`{"enabled":true,"devices":3}`))
	})
	out := capture(t, func() {
		if err := cmdAlertsShow("shop"); err != nil {
			t.Fatal(err)
		}
	})
	want := []string{"GET /v1/projects/shop/alerts", "GET /v1/projects/shop/notifications?limit=5"}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Errorf("requests %v, want %v", seen, want)
	}
	for _, w := range []string{"alerts are on for shop", "3 device(s)", "web is down", "web is back  [resolved]"} {
		if !strings.Contains(out, w) {
			t.Errorf("output is missing %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "web is down  [resolved]") {
		t.Errorf("an open alert was marked resolved:\n%s", out)
	}
	if strings.Contains(out, "NO DEVICE YET") {
		t.Errorf("devices exist, no warning wanted:\n%s", out)
	}
}

func TestAlertsShowOnWithNoDevicesWarns(t *testing.T) {
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/notifications") {
			_, _ = w.Write([]byte(`{"notifications":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"enabled":true,"devices":0}`))
	})
	out := capture(t, func() {
		if err := cmdAlertsShow("shop"); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "NO DEVICE YET") {
		t.Errorf("output:\n%s", out)
	}
}

func TestAlertsTestAndOffHitTheirRoutes(t *testing.T) {
	var seen []string
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = w.Write([]byte(`{"sent":2}`))
	})
	out := capture(t, func() {
		if err := cmdAlertsTest("shop"); err != nil {
			t.Fatal(err)
		}
		if err := cmdAlertsOff("shop"); err != nil {
			t.Fatal(err)
		}
	})
	want := []string{"POST /v1/projects/shop/alerts/test", "DELETE /v1/projects/shop/alerts"}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Errorf("requests %v, want %v", seen, want)
	}
	if !strings.Contains(out, "sent to 2 device(s)") || !strings.Contains(out, "alerts are off for shop") {
		t.Errorf("output:\n%s", out)
	}
}

func TestAlertsTestRefusalsAreExplained(t *testing.T) {
	for code, want := range map[string]string{
		"alerts_off": "gg alerts on shop",
		"no_devices": "https://my.gagarin.cloud/projects/shop/alerts",
	} {
		fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"engine words"}}`))
		})
		err := cmdAlertsTest("shop")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want a message containing %q, got %v", code, want, err)
		}
	}
}
