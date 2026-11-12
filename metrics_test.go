package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const metricsJSON = `{"services":[
 {"service":"api","kind":"container","cpu":{"used":0.12,"limit":0.5},"memory":{"used":536870912,"limit":1073741824},
  "disk":{"used":null,"limit":null},"throttled":0.01,"net_in":1234.5,"net_out":99.1,"requests":3.2,"errors":0.0,"latency_p95":0.142},
 {"service":"db","kind":"resource:postgres","cpu":{"used":null,"limit":null},"memory":{"used":null,"limit":null},
  "disk":{"used":1073741824,"limit":4294967296},"throttled":null,"net_in":null,"net_out":null,"requests":null,"errors":null,"latency_p95":null}],
 "windows":["1h","24h","7d","30d"]}`

func TestMetricsProjectReadsTheProjectRoute(t *testing.T) {
	var path string
	var q url.Values
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		path, q = r.URL.Path, r.URL.Query()
		_, _ = w.Write([]byte(metricsJSON))
	})
	capture(t, func() {
		if err := cmdMetrics("shop", ""); err != nil {
			t.Fatal(err)
		}
	})
	if path != "/v1/projects/shop/metrics" || len(q) != 0 {
		t.Errorf("asked %s?%v", path, q)
	}
}

func TestMetricsTableRendersNullsAsDashes(t *testing.T) {
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(metricsJSON)) })
	out := capture(t, func() {
		if err := cmdMetrics("shop", ""); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"SERVICE", "REQ/S", "5XX", "P95", "0.12/0.5 (24%)", "512.0 MiB/1.00 GiB (50%)", "1.00 GiB/4.00 GiB (25%)", "3.2/s", "142 ms",
		"KIND", "resource:postgres", "THROTTLED", "1%", "NET IN/OUT", "1.2 KiB/s / 99 B/s"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	var db string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "db ") {
			db = l
		}
	}
	if strings.Count(db, " - ") < 2 {
		t.Errorf("db row should show dashes for what was not measured: %q", db)
	}
}

func TestMetricsTableHidesHTTPColumnsWhenNothingIsPublic(t *testing.T) {
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"services":[{"service":"w","kind":"container","cpu":{"used":1,"limit":null},"memory":{"used":null,"limit":null},"disk":{"used":null,"limit":null}}],"windows":[]}`))
	})
	out := capture(t, func() { _ = cmdMetrics("shop", "") })
	if strings.Contains(out, "REQ/S") {
		t.Errorf("no HTTP figures, no HTTP columns:\n%s", out)
	}
}

func TestMetricsServiceSendsTheWindow(t *testing.T) {
	var path string
	var q url.Values
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		path, q = r.URL.Path, r.URL.Query()
		_ = json.NewEncoder(w).Encode(map[string]any{"service": "web", "window": "7d", "step": 3600, "limits": map[string]any{}, "series": map[string]any{}})
	})
	capture(t, func() {
		if err := cmdMetrics("shop/web", "7d"); err != nil {
			t.Fatal(err)
		}
	})
	if path != "/v1/projects/shop/services/web/metrics" || q.Get("window") != "7d" {
		t.Errorf("asked %s?%v", path, q)
	}
	capture(t, func() { _ = cmdMetrics("shop/web", "") })
	if len(q) != 0 {
		t.Errorf("no window given, none sent: %v", q)
	}
}

func TestMetricsWindowNeedsAService(t *testing.T) {
	if err := cmdMetrics("shop", "7d"); err == nil || !strings.Contains(err.Error(), "hint:") {
		t.Errorf("got %v", err)
	}
}

func points(t *testing.T, raw string) []metricsPoint {
	t.Helper()
	var ps []metricsPoint
	if err := json.Unmarshal([]byte(raw), &ps); err != nil {
		t.Fatal(err)
	}
	return ps
}

func TestSummarize(t *testing.T) {
	s, ok := summarize(points(t, `[[1,0.1],[2,0.5],[3,0.3]]`))
	if !ok || s.Last != 0.3 || s.Peak != 0.5 || s.Avg < 0.299 || s.Avg > 0.301 || s.LastAt.Unix() != 3 {
		t.Errorf("got %+v", s)
	}
	if _, ok := summarize(nil); ok {
		t.Error("no points, no summary")
	}
}

// A gap is not a zero: it pulls nothing down, and the last value is the last
// one measured, with its own time.
func TestSummarizeSkipsGaps(t *testing.T) {
	s, ok := summarize(points(t, `[[1,null],[2,100],[3,null]]`))
	if !ok || s.Last != 100 || s.Avg != 100 || s.Peak != 100 || s.LastAt.Unix() != 2 {
		t.Errorf("got %+v", s)
	}
	if _, ok := summarize(points(t, `[[1,null]]`)); ok {
		t.Error("nothing measured, no summary")
	}
}

// Zero means none; something small is never written as zero.
func TestSmallFiguresAreNotZero(t *testing.T) {
	for _, c := range []struct {
		v    float64
		u    metricsUnit
		want string
	}{
		{0.004, unitCores, "<0.01"},
		{0.0004, unitFraction, "<0.1%"},
		{0, unitFraction, "0%"},
		{0.9996, unitSeconds, "1 s"},
		{0.142, unitSeconds, "142 ms"},
	} {
		if got := fmtMetric(c.v, c.u); got != c.want {
			t.Errorf("fmtMetric(%v) = %q, want %q", c.v, got, c.want)
		}
	}
}

func TestMetricsServiceSummaryPrintsLimitsAndPeakShare(t *testing.T) {
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"service":"api","window":"24h","step":300,
		 "limits":{"cpu":0.5,"memory":1073741824,"disk":null},
		 "series":{"cpu":[[1,0.1],[2,0.4],[3,0.25]],"memory":[[1,536870912],[2,805306368]],"latency_p95":[[1,0.2]]}}`))
	})
	out := capture(t, func() {
		if err := cmdMetrics("shop/api", ""); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"shop/api, last 24h", "cpu", "0.25", "0.4", "80%", "768.0 MiB", "200 ms", "LAST", "TAKEN"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "disk") || strings.Contains(out, "throttled") {
		t.Errorf("absent series should not print:\n%s", out)
	}
}

func TestMetricsUnavailableCarriesTheHint(t *testing.T) {
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"error":{"code":"metrics_unavailable","message":"the metrics store did not answer","fix_hint":"try again in a minute"}}`))
	})
	for _, ref := range []string{"shop", "shop/web"} {
		err := cmdMetrics(ref, "")
		if err == nil || !strings.Contains(err.Error(), "[metrics_unavailable]") || !strings.Contains(err.Error(), "hint: try again in a minute") {
			t.Errorf("%s: got %v", ref, err)
		}
	}
}
