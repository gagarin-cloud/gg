package main

// What `gg alerts` sends, and what it tells somebody to do next. The engine
// decides the defaults — ntfy.sh, a topic made up — so the thing to pin here is
// that the CLI does not decide them first by sending empty strings.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

const alertsOn = `{"enabled":true,"server":"https://ntfy.sh","topic":"gagarin-abc","subscribe":"https://ntfy.sh/gagarin-abc"}`

func TestAlertsOnSendsOnlyWhatWasSaid(t *testing.T) {
	var got map[string]any
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/v1/projects/shop/alerts" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(alertsOn))
	})

	out := capture(t, func() {
		if err := cmdAlertsOn("shop", "", "", ""); err != nil {
			t.Fatal(err)
		}
	})
	if len(got) != 0 {
		t.Errorf("a bare `gg alerts on` must leave the defaults to the engine, sent %v", got)
	}
	for _, want := range []string{"https://ntfy.sh/gagarin-abc", "subscribe to topic gagarin-abc", "gg alerts test shop"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "on server") {
		t.Errorf("ntfy.sh is the app's default and needs no mention:\n%s", out)
	}
}

func TestAlertsOnPassesAServerAndToken(t *testing.T) {
	var got map[string]any
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(`{"enabled":true,"server":"https://ntfy.example.com","topic":"ops","subscribe":"https://ntfy.example.com/ops","has_token":true}`))
	})
	out := capture(t, func() {
		if err := cmdAlertsOn("shop", "https://ntfy.example.com", "ops", "tk_x"); err != nil {
			t.Fatal(err)
		}
	})
	if got["server"] != "https://ntfy.example.com" || got["topic"] != "ops" || got["token"] != "tk_x" {
		t.Errorf("sent %v", got)
	}
	if !strings.Contains(out, "on server https://ntfy.example.com") {
		t.Errorf("a server of your own has to be typed into the app too:\n%s", out)
	}
	if strings.Contains(out, "tk_x") {
		t.Errorf("the token was printed back:\n%s", out)
	}
}

func TestAlertsShowSaysHowToTurnThemOn(t *testing.T) {
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"enabled":false}`))
	})
	out := capture(t, func() {
		if err := cmdAlertsShow("shop"); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "alerts are off for shop") || !strings.Contains(out, "gg alerts on shop") {
		t.Errorf("output:\n%s", out)
	}
}

func TestAlertsTestAndOffHitTheirRoutes(t *testing.T) {
	var seen []string
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		_, _ = w.Write([]byte(`{"sent":true,"subscribe":"https://ntfy.sh/gagarin-abc","enabled":false}`))
	})
	_ = capture(t, func() {
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
}
