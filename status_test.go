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

// The pricing page prices in rubles and kopecks, and so does the meter
// watching it — a float here would silently disagree with the invoice by a
// kopeck or two.
func TestUsageTodayIsRublesAndKopecksNotAFloat(t *testing.T) {
	out := capture(t, func() {
		printStatusTable(statusResp{Project: "shop", ProjectID: "9v3juxz0",
			Services:   []serviceStatus{svc("web")},
			UsageToday: usageToday{Kopecks: 1205}})
	})
	if !strings.Contains(out, "₽12.05 today so far") {
		t.Errorf("expected today's usage on screen:\n%s", out)
	}
}
