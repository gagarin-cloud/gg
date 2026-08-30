package main

// Two ways of looking at the same answer.
//
// The table is what `gg status` prints: one row per service, aligned, so a
// project is taken in at a glance rather than read. The graph is what `--visual`
// opens: the same data drawn as the shape it actually has, because "api needs
// db, db needs nothing" is a picture long before it is a list.
//
// Both render the payload the API already returns. Neither computes anything the
// control plane does not already know — if the two ever disagree, the API is
// right and this file is wrong.

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
)

// The page, its two libraries and its stylesheet, compiled into the binary.
//
// Served from here rather than fetched at runtime because this command is most
// useful to somebody debugging a network, which is the worst moment to depend on
// a CDN — and because nobody outside this machine needs to learn the names of a
// project's services. See web/README.md for versions and licences.
//
//go:embed web/index.html web/app.js web/cytoscape.min.js web/dagre.min.js web/cytoscape-dagre.js
var webFS embed.FS

// state reduces a service to the three answers a colour can carry. Deliberately
// coarse: the detail is a column away, and a legend with seven entries is one
// nobody reads.
func state(s serviceStatus) string {
	switch {
	case !s.InSync || !s.Actual.Exists:
		return "out-of-sync"
	case s.Actual.Ready < s.Actual.Desired:
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
	head = append(head, "READY", "PORT", "REACHES", "IMAGE")
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
		mark := map[string]string{"running": "●", "starting": "◐", "out-of-sync": "○"}[state(s)]
		reaches := strings.Join(s.Needs, ", ")
		if reaches == "" {
			reaches = "—"
		}
		row := []string{mark, s.Name}
		if anyResource {
			row = append(row, kindLabel(s.Kind))
		}
		row = append(row,
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
	if seen["out-of-sync"] {
		notes = append(notes, "○ failing")
	}
	fmt.Printf("\n  %s\n", strings.Join(notes, "   "))

	// The one thing the table cannot show, said only when it applies: a service
	// that is not doing what it was told, and what the cluster says about it.
	for _, s := range st.Services {
		if state(s) == "out-of-sync" && s.Actual.Message != "" {
			fmt.Printf("  ○ %s: %s\n", s.Name, s.Actual.Message)
		}
	}
	fmt.Printf("  %s today so far\n", formatRubles(st.UsageToday.Kopecks))
	fmt.Printf("\n  gg status %s --visual   the same thing as a picture\n\n", st.Project)
}

// formatRubles renders kopecks the way the pricing page prices things: rubles
// and kopecks, not a float that would round a few of them away.
func formatRubles(kopecks int64) string {
	return fmt.Sprintf("₽%d.%02d", kopecks/100, kopecks%100)
}

// ---- the visual ---------------------------------------------------------

// serveVisual opens the graph in a browser and keeps serving it until
// interrupted.
//
// The page is served from here rather than written to a file so that it can be
// live: it re-fetches every few seconds, which turns `gg status --visual` into
// something worth leaving open on a second monitor while a deploy converges.
//
// The credential never reaches the browser. The page asks this process for the
// data and this process asks the API, so a bookmarked URL is useless the moment
// the command exits — which is the point.
func serveVisual(project string) error {
	// 127.0.0.1, not :0 on every interface. This serves one person's private
	// project state with no authentication of its own; the only thing making that
	// safe is that nothing off this machine can connect.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("could not open a local port: %w", err)
	}
	url := fmt.Sprintf("http://%s", ln.Addr().String())

	assets, err := fs.Sub(webFS, "web")
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(assets)))
	mux.HandleFunc("/data", func(w http.ResponseWriter, r *http.Request) {
		var raw json.RawMessage
		if err := call("GET", "/v1/projects/"+project+"/status", nil, &raw); err != nil {
			// Reported into the page rather than only to the terminal the user has
			// just tabbed away from.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	})

	// Fetched once before opening a window, so a bad project name or an expired
	// credential is a line in the terminal rather than an error card in a browser
	// tab the user then has to close.
	var probe statusResp
	if err := call("GET", "/v1/projects/"+project+"/status", nil, &probe); err != nil {
		return err
	}

	fmt.Printf("\n  %s — %d service(s)\n", probe.Project, len(probe.Services))
	fmt.Printf("  serving at %s (refreshes every 3s)\n", url)
	fmt.Printf("  press ctrl-c when you are done\n\n")
	openBrowser(url)

	return http.Serve(ln, mux)
}

// openBrowser is best-effort on purpose. Every failure mode here — no desktop, a
// remote shell, an unusual window manager — is one where printing the URL is a
// perfectly good outcome, and none of them is worth failing the command over.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		fmt.Printf("  (could not open a browser — open %s yourself)\n", url)
		return
	}
	go func() { _ = cmd.Wait() }()
}

// A resource's kind is namespaced — "resource:postgres" — so that telling one
// from a service is a property of the value rather than a list to keep in step
// with the control plane.
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
