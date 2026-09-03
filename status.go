package main

// The table `gg status` prints: one row per service, aligned, so a project is
// taken in at a glance rather than read.
//
// It renders the payload the API already returns and computes nothing the
// control plane does not already know — if the two ever disagree, the API is
// right and this file is wrong.
//
// There used to be a second way of looking at the same answer: `--visual`
// served a live dependency graph into a browser, with two graph libraries
// embedded in the binary to draw it. Removed 2026-09-01, the day
// my.gagarin.cloud grew a project workspace whose whole right half is that
// graph — better drawn, always current, behind the same sign-in. One picture,
// maintained once.

import (
	"fmt"
	"strings"
)

// state reduces a service to the three answers a colour can carry. Deliberately
// coarse: the detail is a column away, and a legend with seven entries is one
// nobody reads.
//
// The order is the whole content of this function, and it used to be wrong in a
// way that only showed up once the control plane started telling the truth.
// InSync was tested first, and InSync is false for any service with fewer ready
// pods than it asked for — so with readiness now meaning something, every
// deploy spent its first seconds printing "failing" at somebody who had just
// typed `gg ship` and whose service was fine. A status display that cries wolf
// on the happy path is one whose alarms get ignored.
//
// So "failing" is now reserved for the two cases where waiting will not help:
// there is nothing in the cluster at all, or Kubernetes itself has stopped
// calling this a rollout in progress. Everything short of that — pulling an
// image, booting, an ingress that has not appeared yet — is "starting", which
// is what it is. The reconciler converges the rest within a pass, and a
// divergence it will fix on its own is not a fault to shout about.
func state(s serviceStatus) string {
	switch {
	case !s.Actual.Exists, s.Actual.Stalled:
		return "failing"
	// Nothing is meant to be running, so nothing missing. Today this means the
	// project is suspended, and the notice above the table says why — but the
	// row still has to read as stopped rather than as perpetually starting,
	// which is what a zero replica count looks like to every test above.
	case s.Actual.Desired == 0:
		return "stopped"
	case s.Actual.Ready < s.Actual.Desired, !s.InSync:
		return "starting"
	default:
		return "running"
	}
}

// shortImage drops the part of an image reference that is the same on every row.
//
// gagarin only runs images from a project's own space, so every one of them
// starts `<registry>/<project id>/` — forty-odd characters of prefix repeated
// down the column, pushing the part that differs off the edge of the terminal.
// The full reference is still what was deployed; this is only how it is shown.
func shortImage(image, projectID string) string {
	if i := strings.Index(image, "/"+projectID+"/"); i >= 0 {
		return image[i+len(projectID)+2:]
	}
	return image
}

// ---- the table ----------------------------------------------------------

// line is one printed row.
//
// Two shapes, because the table has two kinds of thing in it. A service is a row
// of cells that line up with every other service's cells. An address is a
// sentence hanging under the service it belongs to, and it must not be a cell:
// column widths come from the content, so a forty-character hostname in the
// SERVICE column would pad every service name out to match it and push the rest
// of the table off the screen to describe one domain.
type line struct {
	// cells is set for a service row, and lines up with the header.
	cells []string
	// text is set for an address, printed under its service and outside the grid.
	text string
}

func printStatusTable(st statusResp) {
	fmt.Printf("\nproject %s  (id %s)\n\n", st.Project, st.ProjectID)

	// Before the table, not after it. These are statements about whether the
	// table means what it appears to mean — a suspended project, a reconciler
	// that has stopped — so they change how every row below should be read, and
	// something that changes how you read a thing has to arrive before it
	// rather than in a footnote.
	for _, n := range st.Notices {
		fmt.Printf("  ! %s\n", n)
	}
	if len(st.Notices) > 0 {
		fmt.Println()
	}
	if len(st.Services) == 0 {
		fmt.Printf("  no services yet — ship one with `gg ship %s/web:8080`\n", st.Project)
		fmt.Println()
		return
	}

	// A volume column only when something has one. Most projects have none, and
	// an always-present column of dashes is noise that makes the real columns
	// harder to find.
	anyVolume := false
	// Likewise a kind column: it only earns its place once a project has
	// something in it that is not a service.
	anyResource := false
	for _, s := range st.Services {
		if s.VolumePath != "" {
			anyVolume = true
		}
		if isResourceKind(s.Kind) {
			anyResource = true
		}
	}

	head := []string{"", "SERVICE"}
	if anyResource {
		head = append(head, "KIND")
	}
	// SIZE is always shown, unlike VOLUME and KIND. Every service has one, so it
	// is never a column of dashes — and its value is highest exactly when it is
	// uniform and wrong: the first question after an out-of-memory kill is what
	// the thing was running at, and needing a second command to find out is
	// friction at the worst moment. It is also a price, and the total at the
	// bottom of this page is unexplainable without it.
	head = append(head, "SIZE", "READY", "PORT", "REACHES", "IMAGE")
	if anyVolume {
		head = append(head, "VOLUME")
	}

	// No URL column. There used to be one, holding the generated address, while a
	// custom domain hung underneath as a sub-row — which said, wrongly, that the
	// two were different kinds of fact. Now every address is a line under its
	// service and the widest column in the table is gone, which is most of why
	// this reads at a glance again.
	lines := []line{{cells: head}}
	for _, s := range st.Services {
		mark := map[string]string{
			"running": "●", "starting": "◐", "failing": "○", "stopped": "◌",
		}[state(s)]
		reaches := strings.Join(s.Needs, ", ")
		if reaches == "" {
			reaches = "—"
		}
		row := []string{mark, s.Name}
		if anyResource {
			row = append(row, kindLabel(s.Kind))
		}
		row = append(row,
			sizeLabel(s.Size),
			fmt.Sprintf("%d/%d", s.Actual.Ready, s.Actual.Desired),
			fmt.Sprintf("%d", s.Port),
			reaches, shortImage(s.Image, st.ProjectID),
		)
		if anyVolume {
			v := "—"
			if s.VolumePath != "" {
				v = fmt.Sprintf("%dGB %s", s.VolumeSizeGB, s.VolumePath)
			}
			row = append(row, v)
		}
		lines = append(lines, line{cells: row})

		// Every address the service answers on, under it, in the order the
		// control plane put them: a name somebody owns first, because that is the
		// one that can be waiting on them.
		for _, d := range s.Domains {
			lines = append(lines, line{text: domainLine(d)})
		}
	}

	// Widths from the content, so nothing is truncated and nothing is padded to a
	// guess. Address lines are skipped: they are not in the grid, which is the
	// whole reason they can be as long as a hostname needs to be.
	w := make([]int, len(head))
	for _, l := range lines {
		for i, c := range l.cells {
			if n := len([]rune(c)); n > w[i] {
				w[i] = n
			}
		}
	}
	for _, l := range lines {
		if l.cells == nil {
			fmt.Println(strings.TrimRight("  "+l.text, " "))
			continue
		}
		var b strings.Builder
		b.WriteString("  ")
		for i, c := range l.cells {
			if i == len(l.cells)-1 {
				b.WriteString(c)
				break
			}
			b.WriteString(c)
			b.WriteString(strings.Repeat(" ", w[i]-len([]rune(c))+2))
		}
		fmt.Println(strings.TrimRight(b.String(), " "))
	}

	// Only mention a state that is actually on screen. A legend explaining
	// symbols nobody is looking at is the sort of thing that trains people to
	// skip legends.
	seen := map[string]bool{}
	for _, s := range st.Services {
		seen[state(s)] = true
	}
	var notes []string
	if seen["running"] {
		notes = append(notes, "● running")
	}
	if seen["starting"] {
		notes = append(notes, "◐ starting")
	}
	if seen["failing"] {
		notes = append(notes, "○ failing")
	}
	if seen["stopped"] {
		notes = append(notes, "◌ stopped")
	}
	fmt.Printf("\n  %s\n", strings.Join(notes, "   "))

	// The one thing the table cannot show, said only when it applies: what the
	// cluster says about a service that is not doing what it was told.
	//
	// Printed for anything still starting as well, not only for failures. The
	// reason a deploy is taking its time is the most useful sentence on the
	// screen while it is taking its time — ImagePullBackOff reads the same
	// whether or not Kubernetes has given up yet, and waiting three minutes to
	// be told about a typo in an image name helps nobody.
	for _, s := range st.Services {
		st := state(s)
		if st == "running" || st == "stopped" || s.Actual.Message == "" {
			continue
		}
		fmt.Printf("  %s %s: %s\n",
			map[string]string{"starting": "◐", "failing": "○"}[st], s.Name, s.Actual.Message)
	}
	fmt.Printf("  %s today so far\n", formatUSD(st.UsageToday.MicroUSD))
	fmt.Println()
}

// formatUSD renders micro-dollars the way the pricing page prices things:
// integer arithmetic, not a float that would round a few millionths away.
//
// Three decimal places, always. Two would be the conventional choice and would
// read "$0.00" for the first three quarters of an hour of a single service —
// a meter that shows nothing while it is running is the one thing this line
// must not do. One format rather than two, so "$1.050" is the price of never
// having a threshold where the readout changes shape.
func formatUSD(microUSD int64) string {
	return fmt.Sprintf("$%d.%03d", microUSD/1_000_000, (microUSD%1_000_000)/1_000)
}

func isResourceKind(kind string) bool { return strings.HasPrefix(kind, "resource:") }

// kindLabel is what the table shows. "service" rather than "container": this
// table is at human altitude, and container is the word the platform uses to
// itself. An empty kind is a service too — that is what an older control plane
// returns.
func kindLabel(kind string) string {
	if isResourceKind(kind) {
		return strings.TrimPrefix(kind, "resource:")
	}
	return "service"
}

// domainLine renders one address as a child of its service.
//
// The marker carries the same vocabulary the service marks use: a filled dot when
// there is nothing left to do, an open one when somebody has to act. A finished
// address says nothing more than its own name — no "ok" column, because a full
// dot and silence already mean it — so the only text here is the text somebody
// needs, which is who is holding the address up and why.
func domainLine(d domainStatus) string {
	mark := "○"
	if d.State == "ok" {
		mark = "●"
	}
	// The URL, not the bare hostname: it is the form a reader can paste into a
	// browser, and terminals make it clickable.
	url := d.URL
	if url == "" {
		url = "https://" + d.Domain
	}
	out := mark + "  └ " + url
	if d.State != "ok" {
		out += "   " + describeDomainState(d.State, d.BlockedOn)
	}
	return out
}

// sizeLabel is the size as it should read in a table. An older control plane
// does not send one, and a blank cell is a better answer than inventing "s" —
// the platform, not this client, decides what a service that never named a size
// gets, and guessing here would be a second opinion about somebody's bill.
func sizeLabel(size string) string {
	if size == "" {
		return "—"
	}
	return size
}
