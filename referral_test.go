package main

// What `gg referral` prints for each state the engine can report. The numbers
// are the engine's; what is checked is that they reach the reader, that a
// not-yet-eligible account is told when invites unlock instead of being handed
// a blank link, and that nothing beyond the contract is shown.

import (
	"net/http"
	"strings"
	"testing"
)

func runReferral(t *testing.T, body string) (string, string) {
	t.Helper()
	var path string
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.Method + " " + r.URL.Path
		_, _ = w.Write([]byte(body))
	})
	var err error
	out := capture(t, func() { err = cmdReferral() })
	if err != nil {
		t.Fatal(err)
	}
	return out, path
}

const referralHead = `"eligible":true,"code":"7K3M9QXA","link":"https://gagarin.cloud/?ref=7K3M9QXA",
"rate_percent":15,"window_months":3,"match_cap_micro_usd":50000000`

func TestReferralEligible(t *testing.T) {
	out, path := runReferral(t, `{`+referralHead+`,
"totals":{"invited":2,"earning_now":1,"invited_topped_up_micro_usd":120000000,"earned_micro_usd":18000000},
"invited":[
 {"display":"alice","joined_at":"2099-10-03T12:00:00Z","window_ends_at":"2100-01-03T12:00:00Z","earning":true,"status":"active","topped_up_micro_usd":100000000,"earned_micro_usd":15000000},
 {"display":"j***@gmail.com","joined_at":"2020-01-01T12:00:00Z","window_ends_at":"2020-04-01T12:00:00Z","earning":false,"status":"active","topped_up_micro_usd":20000000,"earned_micro_usd":3000000}]}`)
	if path != "GET /v1/referrals" {
		t.Errorf("request was %s", path)
	}
	for _, want := range []string{
		"https://gagarin.cloud/?ref=7K3M9QXA", "7K3M9QXA", "gg login --ref 7K3M9QXA",
		"matched up to $50.000", "15%", "3 months", "as credit",
		"2 invited, 1 earning now, $120.000 topped up by them, $18.000 earned by you",
		"USER", "alice", "j***@gmail.com", "$100.000", "$15.000", "ends 2100-01-0", "ended 2020-04-0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

func TestReferralNotEligibleShowsNoLink(t *testing.T) {
	out, _ := runReferral(t, `{"eligible":false,"rate_percent":15,"window_months":3,"match_cap_micro_usd":50000000,
"totals":{"invited":0,"earning_now":0,"invited_topped_up_micro_usd":0,"earned_micro_usd":0},"invited":[]}`)
	if !strings.Contains(out, "invite links unlock after your first top-up") {
		t.Errorf("not eligible must say when links unlock:\n%s", out)
	}
	for _, bad := range []string{"https://gagarin.cloud/?ref", "your code", "gg login --ref"} {
		if strings.Contains(out, bad) {
			t.Errorf("a not-eligible account must not see %q:\n%s", bad, out)
		}
	}
}

func TestReferralEligibleButNobodyInvited(t *testing.T) {
	out, _ := runReferral(t, `{`+referralHead+`,
"totals":{"invited":0,"earning_now":0,"invited_topped_up_micro_usd":0,"earned_micro_usd":0},"invited":[]}`)
	if !strings.Contains(out, "https://gagarin.cloud/?ref=7K3M9QXA") ||
		!strings.Contains(out, "nobody has signed up through your link yet") {
		t.Errorf("unexpected output:\n%s", out)
	}
	if strings.Contains(out, "USER") {
		t.Errorf("an empty list needs no table:\n%s", out)
	}
}

func TestReferralVoidAndEndedRows(t *testing.T) {
	out, _ := runReferral(t, `{`+referralHead+`,
"totals":{"invited":2,"earning_now":0,"invited_topped_up_micro_usd":50000000,"earned_micro_usd":0},
"invited":[
 {"display":"bob","joined_at":"2099-10-03T12:00:00Z","window_ends_at":"2100-01-03T12:00:00Z","earning":false,"status":"void","topped_up_micro_usd":50000000,"earned_micro_usd":0},
 {"display":"carol","joined_at":"2020-01-01T12:00:00Z","window_ends_at":"2020-04-01T12:00:00Z","earning":false,"status":"active","topped_up_micro_usd":0,"earned_micro_usd":0}]}`)
	var bob, carol string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "bob") {
			bob = l
		}
		if strings.HasPrefix(l, "carol") {
			carol = l
		}
	}
	if !strings.HasSuffix(bob, "void") || strings.Contains(bob, "ends ") {
		t.Errorf("a void referral shows no window: %q", bob)
	}
	if !strings.Contains(carol, "ended 2020-04-0") || !strings.HasSuffix(carol, "ended") {
		t.Errorf("a closed window reads ended: %q", carol)
	}
}

func TestReferralShowsAnEngineRefusal(t *testing.T) {
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"no"}}`))
	})
	var err error
	capture(t, func() { err = cmdReferral() })
	if err == nil {
		t.Error("a refused call must surface as an error")
	}
}
