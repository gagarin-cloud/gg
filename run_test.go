package main

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
)

func TestParseTimeout(t *testing.T) {
	for in, want := range map[string]int{
		"1s":    1,
		"45s":   45,
		"5m":    300,
		"1h":    3600,
		"60m":   3600,
		"1m30s": 90,
	} {
		t.Run(in, func(t *testing.T) {
			got, err := parseTimeout(in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != want {
				t.Errorf("got %d, want %d", got, want)
			}
		})
	}
}

func TestParseTimeoutRefuses(t *testing.T) {
	for _, in := range []string{"1.5s", "500ms", "0s", "-5s", "61m", "3601s", "2h", "abc", "90"} {
		t.Run(in, func(t *testing.T) {
			if got, err := parseTimeout(in); err == nil {
				t.Errorf("accepted %q as %d seconds", in, got)
			}
		})
	}
}

func TestRunFlagsFinishTimeout(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		want    int
		wantErr bool
	}{
		{nil, 0, false},
		{[]string{"--timeout", "5m"}, 300, false},
		{[]string{"--timeout", "2h"}, 0, true},
	} {
		fs := pflag.NewFlagSet("run", pflag.ContinueOnError)
		v := bindRunFlags(fs)
		if err := fs.Parse(tc.args); err != nil {
			t.Fatal(err)
		}
		f, err := v.finish()
		if (err != nil) != tc.wantErr {
			t.Fatalf("%v: err = %v", tc.args, err)
		}
		if err == nil && f.timeout != tc.want {
			t.Errorf("%v: timeout = %d, want %d", tc.args, f.timeout, tc.want)
		}
	}
}

func TestRunBodyCarriesTimeoutOnlyWhenSet(t *testing.T) {
	without := runBody("img", "sha256:x", &runFlags{env: map[string]string{}})
	if _, ok := without["timeout_seconds"]; ok {
		t.Errorf("timeout_seconds sent when --timeout was not given: %v", without)
	}
	with := runBody("img", "sha256:x", &runFlags{env: map[string]string{}, timeout: 90})
	if got := with["timeout_seconds"]; !reflect.DeepEqual(got, 90) {
		t.Errorf("timeout_seconds = %#v, want 90", got)
	}
}

func TestParseSchedule(t *testing.T) {
	for _, tc := range []struct {
		in      string
		wantErr string
	}{
		{"* * * * *", ""},
		{"0 3 * * *", ""},
		{"*/5 9-17 * * 1-5", ""},
		{"@hourly", ""},
		{"@daily", ""},
		{"@weekly", ""},
		{"@monthly", ""},
		{"@yearly", ""},
		{"@annually", ""},
		{"@midnight", ""},
		{"", "cannot be removed"},
		{"   ", "cannot be removed"},
		{"CRON_TZ=Europe/Berlin 0 3 * * *", "--tz"},
		{"TZ=UTC 0 3 * * *", "--tz"},
		{"@every 1h", "not supported"},
		{"off", "not a cron expression"},
		{"60 * * * *", "not a cron expression"},
		{"0 3 * *", "not a cron expression"},
		{"0 0 3 * * *", "not a cron expression"},
		{"banana", "not a cron expression"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseSchedule(tc.in)
			if tc.wantErr == "" {
				if err != nil || got != strings.TrimSpace(tc.in) {
					t.Fatalf("got %q, %v", got, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestParseTimeZone(t *testing.T) {
	for _, in := range []string{"Europe/Berlin", "America/New_York", "UTC"} {
		if got, err := parseTimeZone(in); err != nil || got != in {
			t.Errorf("%q: %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "Local", "Mars/Olympus", "CET5CEST4", "berlin time"} {
		if got, err := parseTimeZone(in); err == nil {
			t.Errorf("accepted %q as %q", in, got)
		}
	}
}

func TestRunFlagsFinishSchedule(t *testing.T) {
	for _, tc := range []struct {
		args        []string
		sched, zone string
		wantErr     bool
	}{
		{nil, "", "", false},
		{[]string{"--schedule", "0 3 * * *"}, "0 3 * * *", "", false},
		{[]string{"--schedule", "@daily", "--tz", "Europe/Berlin"}, "@daily", "Europe/Berlin", false},
		{[]string{"--tz", "Europe/Berlin"}, "", "Europe/Berlin", false},
		{[]string{"--schedule", ""}, "", "", true},
		{[]string{"--schedule", "nope"}, "", "", true},
		{[]string{"--tz", "Nowhere/Land"}, "", "", true},
		{[]string{"--tz", ""}, "", "", true},
	} {
		fs := pflag.NewFlagSet("run", pflag.ContinueOnError)
		v := bindRunFlags(fs)
		if err := fs.Parse(tc.args); err != nil {
			t.Fatal(err)
		}
		f, err := v.finish()
		if (err != nil) != tc.wantErr {
			t.Fatalf("%v: err = %v", tc.args, err)
		}
		if err == nil && (f.schedule != tc.sched || f.timeZone != tc.zone) {
			t.Errorf("%v: got %q/%q, want %q/%q", tc.args, f.schedule, f.timeZone, tc.sched, tc.zone)
		}
	}
}

func TestRunBodyCarriesScheduleOnlyWhenSet(t *testing.T) {
	without := runBody("img", "sha256:x", &runFlags{env: map[string]string{}})
	for _, k := range []string{"schedule", "time_zone"} {
		if _, ok := without[k]; ok {
			t.Errorf("%s sent when its flag was not given: %v", k, without)
		}
	}
	with := runBody("img", "sha256:x", &runFlags{env: map[string]string{}, schedule: "0 3 * * *", timeZone: "Europe/Berlin"})
	if with["schedule"] != "0 3 * * *" || with["time_zone"] != "Europe/Berlin" {
		t.Errorf("body = %v", with)
	}
	tzOnly := runBody("img", "sha256:x", &runFlags{env: map[string]string{}, timeZone: "Europe/Berlin"})
	if _, ok := tzOnly["schedule"]; ok {
		t.Errorf("schedule sent with only --tz: %v", tzOnly)
	}
}

func TestNextRun(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	got, err := nextRun("0 3 * * *", "", now)
	if err != nil || !got.Equal(time.Date(2026, 10, 9, 3, 0, 0, 0, time.UTC)) {
		t.Errorf("UTC: %v, %v", got, err)
	}
	// 03:00 in Berlin (UTC+2 in October) is 01:00 UTC.
	got, err = nextRun("0 3 * * *", "Europe/Berlin", now)
	if err != nil || !got.Equal(time.Date(2026, 10, 9, 1, 0, 0, 0, time.UTC)) {
		t.Errorf("Berlin: %v, %v", got, err)
	}
}

// A scheduled job's answer ends gg's part: it must print the schedule and
// return without asking how a run ended, with or without --detach.
func TestRunOfAScheduledJobDoesNotWait(t *testing.T) {
	for _, detach := range []bool{false, true} {
		var gets []string
		var put map[string]any
		fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/v1/whoami":
				_, _ = w.Write([]byte(`{"platform":"linux/amd64","registry":"reg.example"}`))
			case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/status"):
				gets = append(gets, r.URL.Path)
				http.Error(w, "polled", 500)
			case r.Method == "GET":
				_, _ = w.Write([]byte(`{"id":"9v3juxz0","name":"shop"}`))
			case r.Method == "PUT":
				_ = json.NewDecoder(r.Body).Decode(&put)
				_, _ = w.Write([]byte(`{"name":"report","revision":2,"schedule":"0 3 * * *","time_zone":"Europe/Berlin"}`))
			default:
				http.NotFound(w, r)
			}
		})
		var err error
		out := capture(t, func() {
			err = cmdRun("shop/report", "report:v2", &runFlags{
				env: map[string]string{}, detach: detach,
				schedule: "0 3 * * *", timeZone: "Europe/Berlin",
			})
		})
		if err != nil {
			t.Fatalf("detach=%v: %v", detach, err)
		}
		if len(gets) != 0 {
			t.Errorf("detach=%v: polled status: %v", detach, gets)
		}
		if put["schedule"] != "0 3 * * *" || put["time_zone"] != "Europe/Berlin" {
			t.Errorf("PUT body = %v", put)
		}
		if !strings.Contains(out, `scheduled "0 3 * * *" (Europe/Berlin), next run `) {
			t.Errorf("output lacks the schedule line:\n%s", out)
		}
	}
}
