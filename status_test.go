package main

// What the table looks like.
//
// Untested until the URL column came out, and that change is exactly the kind
// that regresses silently: alignment is computed from content, so one long
// hostname in the wrong place deforms every other row and nothing fails.

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// capture runs f with stdout redirected, because printStatusTable writes rather
// than returns. Worth the plumbing: the thing being asserted is the text.
func capture(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prev := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = prev }()

	done := make(chan string)
	go func() {
		var b bytes.Buffer
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	f()
	_ = w.Close()
	return <-done
}

func svc(name string, domains ...domainStatus) serviceStatus {
	s := serviceStatus{
		Name: name, Image: "reg/p/" + name + ":1", Port: 8080,
		InSync: true, Domains: domains,
	}
	s.Actual.Exists, s.Actual.Ready, s.Actual.Desired = true, 1, 1
	return s
}

func generated(url string) domainStatus {
	return domainStatus{URL: url, Domain: strings.TrimPrefix(url, "https://"),
		Generated: true, State: "ok"}
}

// A private service says nothing about addresses at all. There is no "— private"
// placeholder any more: privacy is the absence of an address, and a column of
// dashes was noise on every project that has one.
func TestAPrivateServiceGetsNoAddressLine(t *testing.T) {
	out := capture(t, func() {
		printStatusTable(statusResp{Project: "shop", ProjectID: "9v3juxz0",
			Services: []serviceStatus{svc("worker")}})
	})
	if strings.Contains(out, "└") {
		t.Errorf("a private service was given an address line:\n%s", out)
	}
	if strings.Contains(out, "URL") {
		t.Errorf("the URL column is back:\n%s", out)
	}
	if strings.Contains(out, "private") {
		t.Errorf("privacy is the absence of an address, not a word in a cell:\n%s", out)
	}
}

// The order is the point: a name somebody owns comes first, because it is the one
// that can be waiting on them. The generated address is last, because it works.
func TestAddressesHangUnderTheirServiceInOrder(t *testing.T) {
	out := capture(t, func() {
		printStatusTable(statusResp{Project: "shop", ProjectID: "9v3juxz0",
			Services: []serviceStatus{
				svc("api"),
				svc("web",
					domainStatus{URL: "https://feed.gagarin.cloud", Domain: "feed.gagarin.cloud", State: "ok"},
					generated("https://web-9v3juxz0.apps.gagarin.cloud"),
				),
			}})
	})
	lines := strings.Split(out, "\n")
	var idx []int
	for i, l := range lines {
		if strings.Contains(l, "└") {
			idx = append(idx, i)
		}
	}
	if len(idx) != 2 {
		t.Fatalf("expected two address lines, got %d:\n%s", len(idx), out)
	}
	if !strings.Contains(lines[idx[0]], "https://feed.gagarin.cloud") {
		t.Errorf("the custom name is not first:\n%s", out)
	}
	if !strings.Contains(lines[idx[1]], "web-9v3juxz0") {
		t.Errorf("the generated address is not last:\n%s", out)
	}
	// Under web, not under api — they must follow the service they belong to.
	web := -1
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "●") && strings.Contains(l, " web ") {
			web = i
		}
	}
	if web < 0 || idx[0] != web+1 {
		t.Errorf("addresses did not follow their service:\n%s", out)
	}
}

// A finished address says nothing but its own name. The full dot already means
// "nobody has to do anything", and a column of "ok" next to it is the sort of
// thing that trains people to stop reading the column.
func TestAFinishedAddressCarriesNoStateText(t *testing.T) {
	out := capture(t, func() {
		printStatusTable(statusResp{Project: "shop", ProjectID: "9v3juxz0",
			Services: []serviceStatus{svc("web", generated("https://web-9v3juxz0.apps.gagarin.cloud"))}})
	})
	for _, l := range strings.Split(out, "\n") {
		if !strings.Contains(l, "└") {
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(l), "●") {
			t.Errorf("a finished address is not marked done: %q", l)
		}
		if strings.Contains(l, "ok") {
			t.Errorf("a finished address printed a state: %q", l)
		}
	}
}

// And a drifting one says who is holding it up, which is the part a reader acts
// on. Without this the open dot would be a puzzle rather than an instruction.
func TestADriftingAddressSaysWhoIsHoldingItUp(t *testing.T) {
	out := capture(t, func() {
		printStatusTable(statusResp{Project: "shop", ProjectID: "9v3juxz0",
			Services: []serviceStatus{svc("web",
				domainStatus{URL: "https://shop.example.com", Domain: "shop.example.com",
					State: "waiting_dns", BlockedOn: "you"},
			)}})
	})
	var line string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "└") {
			line = l
		}
	}
	if !strings.HasPrefix(strings.TrimSpace(line), "○") {
		t.Errorf("a drifting address is marked as done: %q", line)
	}
	if !strings.Contains(line, "waiting for DNS (you)") {
		t.Errorf("the line does not say who is holding it up: %q", line)
	}
}

// The regression the sub-row shape exists to prevent: an address is not a cell,
// so a long hostname must not stretch the columns the services line up in.
func TestALongAddressDoesNotStretchTheTable(t *testing.T) {
	short := capture(t, func() {
		printStatusTable(statusResp{Project: "shop", ProjectID: "9v3juxz0",
			Services: []serviceStatus{svc("web"), svc("api")}})
	})
	long := capture(t, func() {
		printStatusTable(statusResp{Project: "shop", ProjectID: "9v3juxz0",
			Services: []serviceStatus{
				svc("web", generated("https://web-9v3juxz0.this-is-a-very-long-base-domain.example.com")),
				svc("api"),
			}})
	})
	row := func(out string) string {
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "●") && strings.Contains(l, " api ") {
				return l
			}
		}
		return ""
	}
	if row(short) == "" {
		t.Fatalf("could not find the api row:\n%s", short)
	}
	if row(short) != row(long) {
		t.Errorf("an address changed the layout of an unrelated service:\n%q\n%q",
			row(short), row(long))
	}
}

// The pricing page prices in integer micro-dollars, and so does the meter
// watching it — a float here would silently disagree with the invoice.
func TestUsageTodayIsIntegerMicroDollarsNotAFloat(t *testing.T) {
	out := capture(t, func() {
		printStatusTable(statusResp{Project: "shop", ProjectID: "9v3juxz0",
			Services:   []serviceStatus{svc("web")},
			UsageToday: usageToday{MicroUSD: 12_050_000}})
	})
	if !strings.Contains(out, "$12.050 today so far") {
		t.Errorf("expected today's usage on screen:\n%s", out)
	}
}

// One running hour is $0.014, and that has to be legible. Two decimal places
// would print "$0.01"; the first forty-two minutes would print "$0.00".
func TestUsageTodayStaysLegibleBelowACent(t *testing.T) {
	out := capture(t, func() {
		printStatusTable(statusResp{Project: "shop", ProjectID: "9v3juxz0",
			Services:   []serviceStatus{svc("web")},
			UsageToday: usageToday{MicroUSD: 14_000}})
	})
	if !strings.Contains(out, "$0.014 today so far") {
		t.Errorf("an hour of one service should read as $0.014:\n%s", out)
	}
}

// The regression that arrived with the readiness probe. InSync is false for
// any service with fewer ready pods than it asked for, and it used to be
// tested first — so once readiness meant something, every ordinary deploy
// spent its first seconds telling the user their service had failed. A status
// display that cries wolf on the happy path is one whose alarms get ignored.
func TestAServiceThatIsStillStartingIsNotCalledFailing(t *testing.T) {
	s := svc("web")
	s.InSync = false
	s.Actual.Ready, s.Actual.Desired = 0, 1
	s.Actual.Stalled = false
	s.Actual.Message = "the container is running but nothing is listening on port 8080 yet"

	if got := state(s); got != "starting" {
		t.Errorf("a deploy in flight reported as %q", got)
	}
	out := capture(t, func() {
		printStatusTable(statusResp{Project: "shop", ProjectID: "9v3juxz0",
			Services: []serviceStatus{s}})
	})
	if !strings.Contains(out, "◐ starting") {
		t.Errorf("the legend did not say starting:\n%s", out)
	}
	if strings.Contains(out, "failing") {
		t.Errorf("a service that is merely starting was called failing:\n%s", out)
	}
	// And the reason is still on screen, because why a deploy is taking its
	// time is the most useful sentence there while it is taking its time.
	if !strings.Contains(out, "nothing is listening on port 8080") {
		t.Errorf("the cluster's own reason was withheld while starting:\n%s", out)
	}
}

// Once Kubernetes has stopped calling it a rollout in progress, waiting will
// not help and the display should stop implying it might.
func TestAStalledRolloutIsCalledFailing(t *testing.T) {
	s := svc("web")
	s.InSync = false
	s.Actual.Ready, s.Actual.Desired = 0, 1
	s.Actual.Stalled = true
	s.Actual.Message = "the container is running but nothing is listening on port 8080"

	if got := state(s); got != "failing" {
		t.Errorf("a rollout Kubernetes gave up on reported as %q", got)
	}
	out := capture(t, func() {
		printStatusTable(statusResp{Project: "shop", ProjectID: "9v3juxz0",
			Services: []serviceStatus{s}})
	})
	if !strings.Contains(out, "○ failing") {
		t.Errorf("a stalled rollout was not called failing:\n%s", out)
	}
	if !strings.Contains(out, "○ web: the container is running") {
		t.Errorf("the reason was not printed:\n%s", out)
	}
}

// Nothing in the cluster at all is a failure whatever the numbers say.
func TestAServiceWithNoDeploymentIsFailing(t *testing.T) {
	s := svc("web")
	s.Actual.Exists = false
	if got := state(s); got != "failing" {
		t.Errorf("a service with nothing in the cluster reported as %q", got)
	}
}

// A settled service is still just running, or the reordering above has made
// the common case wrong to fix the rare one.
func TestASettledServiceIsRunning(t *testing.T) {
	if got := state(svc("web")); got != "running" {
		t.Errorf("a healthy service reported as %q", got)
	}
}

// The two days in August when both CRON triggers were PAUSED and nothing said
// so. The table is live cluster state and stays true throughout — what stops
// being true is that anything is closing the gap between it and what was asked
// for, which is why this prints above the table rather than beneath it.
func TestASilentReconcilerIsSaidBeforeTheTable(t *testing.T) {
	st := statusResp{Project: "shop", ProjectID: "9v3juxz0",
		Services: []serviceStatus{svc("web")}}
	st.Notices = []string{"the reconciler last ran 2 days ago, so the cluster may have drifted from what you asked for"}

	out := capture(t, func() { printStatusTable(st) })

	if !strings.Contains(out, "2 days ago") {
		t.Errorf("the platform said it had stopped converging and the CLI did not repeat it:\n%s", out)
	}
	warn := strings.Index(out, "2 days ago")
	table := strings.Index(out, "SERVICE")
	if warn > table {
		t.Errorf("the warning printed after the table it changes the reading of:\n%s", out)
	}
}

// Silence when healthy. A line reporting "everything is fine" on every run is
// one people stop reading, and it would then be unread in exactly the way that
// caused this.
func TestAHealthyPlatformAddsNoLine(t *testing.T) {
	out := capture(t, func() {
		printStatusTable(statusResp{Project: "shop", ProjectID: "9v3juxz0",
			Services: []serviceStatus{svc("web")}})
	})
	if strings.Contains(out, "!") {
		t.Errorf("a healthy platform printed a warning line:\n%s", out)
	}
}

// A suspended project is stopped, not perpetually starting. Zero replicas looks
// identical to "on its way" to every other test in this function, and a table
// that says a stopped service is starting is one that will be watched for a
// change that is never coming.
func TestASuspendedProjectReadsAsStoppedWithAReason(t *testing.T) {
	s := svc("web")
	s.InSync = false
	s.Actual.Ready, s.Actual.Desired = 0, 0

	if got := state(s); got != "stopped" {
		t.Errorf("a service with nothing meant to be running reported as %q", got)
	}
	out := capture(t, func() {
		printStatusTable(statusResp{Project: "shop", ProjectID: "9v3juxz0",
			Services: []serviceStatus{s},
			Notices:  []string{"this project is suspended and nothing is running: relaying mail"}})
	})
	if !strings.Contains(out, "◌ stopped") {
		t.Errorf("the legend did not offer a stopped state:\n%s", out)
	}
	if !strings.Contains(out, "relaying mail") {
		t.Errorf("a table of zeroes was printed with no reason for them:\n%s", out)
	}
	if strings.Contains(out, "starting") {
		t.Errorf("a stopped service was called starting:\n%s", out)
	}
}

// --- externals in the table ------------------------------------------------

func external(name string) serviceStatus {
	return serviceStatus{Name: name, Kind: "resource:external"}
}

// The row an external gets. Without the state case it reads "failing" forever —
// there is no Deployment, so Actual.Exists is false — and a reader goes looking
// for a pod that was never meant to exist.
func TestAnExternalDoesNotReadAsFailing(t *testing.T) {
	out := capture(t, func() {
		printStatusTable(statusResp{
			Project: "shop", ProjectID: "p1",
			Services: []serviceStatus{svc("web"), external("openai")},
		})
	})
	if !strings.Contains(out, "\u25c6") {
		t.Errorf("an external needs a mark from outside the pod-state set:\n%s", out)
	}
	if strings.Contains(out, "\u25cb failing") {
		t.Errorf("an external is being reported as a failure:\n%s", out)
	}
	// The pod-shaped columns say "not applicable" rather than zero: a 0 port
	// invites somebody to go looking for a listener.
	if strings.Contains(out, "0/0") {
		t.Errorf("an external was given a readiness count:\n%s", out)
	}
	// The prefix is derived from the name, so a reader who does not know the
	// rule cannot guess it.
	if !strings.Contains(out, "publishes OPENAI_*") {
		t.Errorf("the table does not say what the external publishes:\n%s", out)
	}
}

// The legend explains only what is on screen, and for an external it says what
// the thing is rather than how it is — plus the one clause that stops the table
// implying a guarantee it does not make.
func TestTheExternalLegendSaysItGrantsNoEgress(t *testing.T) {
	out := capture(t, func() {
		printStatusTable(statusResp{
			Project: "shop", ProjectID: "p1",
			Services: []serviceStatus{external("openai")},
		})
	})
	if !strings.Contains(out, "\u25c6 external") {
		t.Errorf("no legend entry for the external mark:\n%s", out)
	}
	if !strings.Contains(out, "not egress") {
		t.Errorf("the legend lets the table imply an egress guarantee:\n%s", out)
	}
}

// And it is absent when nothing on screen is one.
func TestNoExternalLegendWithoutAnExternal(t *testing.T) {
	out := capture(t, func() {
		printStatusTable(statusResp{
			Project: "shop", ProjectID: "p1",
			Services: []serviceStatus{svc("web")},
		})
	})
	if strings.Contains(out, "\u25c6") {
		t.Errorf("a legend explained a symbol that is not on screen:\n%s", out)
	}
}

func TestExternalStateAndKindLabel(t *testing.T) {
	if got := state(external("openai")); got != "external" {
		t.Errorf("state = %q, want external", got)
	}
	if got := kindLabel("resource:external"); got != "external" {
		t.Errorf("kindLabel = %q, want external", got)
	}
	if !isExternalKind("resource:external") {
		t.Error("resource:external is not recognised as one")
	}
	for _, kind := range []string{"resource:postgres", "", "external"} {
		if isExternalKind(kind) {
			t.Errorf("%q was taken for an external", kind)
		}
	}
	// Still a resource, so the KIND column appears for a project holding one.
	if !isResourceKind("resource:external") {
		t.Error("an external must still count as a resource for the KIND column")
	}
}

// An edge to an external and an edge to a database do not mean the same thing —
// one is enforced by a NetworkPolicy, the other is a note about who holds a key
// — so the cell that lists them has to distinguish them. Otherwise a reader
// concludes the graph guarantees the same thing about both.
func TestAnEdgeToAnExternalIsMarked(t *testing.T) {
	bot := svc("bot")
	bot.Needs = []string{"openai", "pg"}
	out := capture(t, func() {
		printStatusTable(statusResp{
			Project: "shop", ProjectID: "p1",
			Services: []serviceStatus{bot, external("openai"),
				{Name: "pg", Kind: "resource:postgres"}},
		})
	})
	if !strings.Contains(out, "openai◆") {
		t.Errorf("an edge to an external is indistinguishable from an enforced one:\n%s", out)
	}
	if strings.Contains(out, "pg◆") {
		t.Errorf("an edge to a database was marked as inventory:\n%s", out)
	}
}
