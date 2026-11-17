package main

import (
	"strings"
	"testing"
)

func TestReplicasReadsACountOrARange(t *testing.T) {
	for in, want := range map[string]replicaRange{"2": {2, 2}, "1-5": {1, 5}, " 3-3 ": {3, 3}} {
		var r replicaRange
		if err := r.Set(in); err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if r != want {
			t.Errorf("%q = %v, want %v", in, r, want)
		}
	}
	for _, in := range []string{"", "0", "-1", "5-1", "two", "1-", "1-x", "x-3", "1-2-3", "+2", "1--5", "+1-+3"} {
		var r replicaRange
		if err := r.Set(in); err == nil {
			t.Errorf("%q was accepted as %v", in, r)
		}
	}
}

// Through the flag set, as `gg deploy` and `gg ship` both parse it, and into
// the body as the pair the API takes.
func TestDeploySendsTheReplicaPair(t *testing.T) {
	f, err := parseDeploy([]string{"--replicas", "1-4"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := captureBody(t, func() error { return deployImage("shop", "api", 8080, target(), f) })
	if err != nil {
		t.Fatal(err)
	}
	if body["min_replicas"] != float64(1) || body["max_replicas"] != float64(4) {
		t.Errorf("replicas in the body = %v-%v, want 1-4", body["min_replicas"], body["max_replicas"])
	}
}

// Absent keeps what the service has, so it is not sent at all: sending 1-1
// would take a service with three copies down to one on every redeploy that
// forgot the flag.
func TestDeployWithoutReplicasOmitsThem(t *testing.T) {
	body, err := captureBody(t, func() error {
		return deployImage("shop", "api", 8080, target(), &deployFlags{env: map[string]string{}})
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"min_replicas", "max_replicas"} {
		if _, present := body[k]; present {
			t.Errorf("a deploy with no --replicas sent %s", k)
		}
	}
}

// Refused while the flags are parsed, before a build and before any request:
// nothing is declared, so nothing is billed.
func TestABadReplicaCountFailsBeforeAnythingIsSent(t *testing.T) {
	for _, args := range [][]string{
		{"--replicas", "4-2"},
		{"--replicas", "2", "--volume", "/data"},
		{"--replicas", "1-3", "--volume", "/data"},
	} {
		if _, err := parseDeploy(args); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
	if _, err := parseDeploy([]string{"--replicas", "1", "--volume", "/data"}); err != nil {
		t.Errorf("one copy with a volume is what every volume service is: %v", err)
	}
}

// A copy the autoscaler asked for and that is not up yet is not a rollout:
// the service is running on the rest.
func TestAScaleUpIsNotStarting(t *testing.T) {
	web := svc("web")
	web.MinReplicas, web.MaxReplicas = 1, 5
	web.Actual.Ready, web.Actual.Desired = 2, 3
	if got := state(web); got != "running" {
		t.Errorf("a range adding a copy reads %q", got)
	}
	fixed := svc("api")
	fixed.MinReplicas, fixed.MaxReplicas = 3, 3
	fixed.Actual.Ready, fixed.Actual.Desired = 2, 3
	if got := state(fixed); got != "starting" {
		t.Errorf("a fixed count short a copy reads %q", got)
	}
	web.InSync = false
	if got := state(web); got != "starting" {
		t.Errorf("a range the control plane calls out of sync reads %q", got)
	}
}

// The column appears once something may run more than one copy, and says the
// setting; READY beside it says how many are up.
func TestStatusShowsReplicasOnlyWhenSomethingHasMore(t *testing.T) {
	one := statusResp{Project: "shop", ProjectID: "9v3juxz0", Services: []serviceStatus{svc("web")}}
	if out := capture(t, func() { printStatusTable(one) }); strings.Contains(out, "REPLICAS") {
		t.Errorf("a project of single copies got a column of ones:\n%s", out)
	}

	web := svc("web")
	web.MinReplicas, web.MaxReplicas = 1, 5
	web.Actual.Ready, web.Actual.Desired = 3, 3
	api := svc("api")
	api.MinReplicas, api.MaxReplicas = 2, 2
	many := statusResp{Project: "shop", ProjectID: "9v3juxz0", Services: []serviceStatus{api, web}}
	out := capture(t, func() { printStatusTable(many) })
	if !strings.Contains(out, "REPLICAS") {
		t.Fatalf("no replicas column:\n%s", out)
	}
	for _, want := range []string{"1-5", "3/3"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	// By column, not by substring: every row must put its REPLICAS cell under
	// the header, a job's and an external's included, or every column after it
	// is shifted.
	job := svc("nightly")
	job.Kind = "job"
	ext := svc("openai")
	ext.Kind, ext.Image = "resource:external", ""
	many.Services = append(many.Services, job, ext)
	out = capture(t, func() { printStatusTable(many) })
	col := -1
	want := map[string]string{"api": "2", "web": "1-5", "nightly": "—", "openai": "—"}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if col < 0 {
			for i, h := range f {
				if h == "REPLICAS" {
					col = i + 1 // the state mark has no header
				}
			}
			continue
		}
		if len(f) < 2 {
			continue
		}
		if w, ok := want[f[1]]; ok {
			if len(f) <= col || f[col] != w {
				t.Errorf("%s: REPLICAS cell is not %q: %q", f[1], w, l)
			}
			delete(want, f[1])
		}
	}
	if len(want) > 0 {
		t.Errorf("rows not found: %v\n%s", want, out)
	}
}
