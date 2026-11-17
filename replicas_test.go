package main

import (
	"strings"
	"testing"
)

func TestReplicasReadsACountOrARange(t *testing.T) {
	for in, want := range map[string][2]int{"2": {2, 2}, "1-5": {1, 5}, " 3-3 ": {3, 3}} {
		lo, hi, err := parseReplicas(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if [2]int{lo, hi} != want {
			t.Errorf("%q = %d-%d, want %d-%d", in, lo, hi, want[0], want[1])
		}
	}
	for _, in := range []string{"", "0", "-1", "5-1", "two", "1-", "1-x", "1-2-3"} {
		if _, _, err := parseReplicas(in); err == nil {
			t.Errorf("%q was accepted", in)
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

func TestABadReplicaCountFailsBeforeAnythingIsSent(t *testing.T) {
	if _, err := parseDeploy([]string{"--replicas", "4-2"}); err == nil {
		t.Error("a backwards range was accepted")
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
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "api") && !strings.Contains(l, " 2 ") {
			t.Errorf("a fixed count of two did not read as 2: %q", l)
		}
	}
}
