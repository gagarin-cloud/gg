package main

// Who may talk to whom.
//
// Every service in a project is default-denied. A private service is reachable
// only from the services that have declared they need it, and — this is the part
// that costs people an afternoon — an undeclared call is *dropped*, not refused.
// It does not come back with "connection refused". It hangs until the client's
// timeout, which for a database driver can be thirty seconds or forever.
//
// This used to be `--needs` on `gg deploy`, replaced wholesale on every deploy.
// Which meant a redeploy that did not restate a dependency withdrew it, and the
// service did not break — it went quiet. `gg domain` exists as its own verb for
// exactly this reason, spelled out in its own file: a flag can release something
// by being forgotten, and that failure is invisible from the inside.
//
// So a dependency is a standing declaration now, like a domain and like a
// resource. A deploy neither grants one nor takes one away.
//
// What it grants is a network path and nothing else. Credentials are still
// passed as environment on the deploy — `gg deps add api db` opens the route to
// the database and gives `api` no way to authenticate with it, which is
// deliberate: a value that appeared from somewhere other than the deploy would
// make a deploy stop describing what it runs.

import (
	"fmt"
	"sort"
	"strings"
)

// serviceNeeds reads the current set. There is no dedicated read endpoint
// because there is no second source of truth: status reports the graph the
// platform is actually enforcing, not a description of it.
func serviceNeeds(project, service string) ([]string, error) {
	var st statusResp
	if err := call("GET", "/v1/projects/"+project+"/status", nil, &st); err != nil {
		return nil, err
	}
	for _, s := range st.Services {
		if s.Name == service {
			return s.Needs, nil
		}
	}
	return nil, fmt.Errorf("%s has no service or resource called %s\n  hint: gg status %s lists them", project, service, project)
}

// setNeeds writes the whole set. The endpoint replaces rather than merges,
// because "what may this service reach" is a question with one answer — an add
// verb and a remove verb that each sent a delta would be two ways to get to a
// state that only one of them could describe.
func setNeeds(project, service string, needs []string) error {
	var out struct {
		Needs    []string `json:"needs"`
		Sentence string   `json:"sentence"`
	}
	path := fmt.Sprintf("/v1/projects/%s/services/%s/needs", project, service)
	if err := call("PUT", path, map[string]any{"needs": needs}, &out); err != nil {
		return err
	}
	if out.Sentence != "" {
		fmt.Println(out.Sentence)
	}
	return nil
}

func cmdDepsList(ref string) error {
	project, service, _, err := parseService(ref)
	if err != nil {
		return err
	}
	needs, err := serviceNeeds(project, service)
	if err != nil {
		return err
	}
	if len(needs) == 0 {
		fmt.Printf("%s reaches nothing else in %s.\n", service, project)
		fmt.Printf("\n  gg deps add %s/%s <service>\n", project, service)
		return nil
	}
	sort.Strings(needs)
	for _, n := range needs {
		fmt.Println(n)
	}
	return nil
}

// cmdDepsAdd declares more. It reads the current set first and sends the union,
// so that adding one dependency cannot drop another — the endpoint replaces, and
// a caller who only knows the name they are adding must not have to know the
// rest to keep them.
func cmdDepsAdd(ref string, add []string) error {
	project, service, _, err := parseService(ref)
	if err != nil {
		return err
	}
	if err := checkNames(add); err != nil {
		return err
	}
	current, err := serviceNeeds(project, service)
	if err != nil {
		return err
	}
	return setNeeds(project, service, mergeNames(current, add, nil))
}

// cmdDepsRemove withdraws. It says plainly when a name was not there rather than
// reporting success: "removed" about something that was never declared reads as
// a fix, and the caller stops looking for the real one.
func cmdDepsRemove(ref string, drop []string) error {
	project, service, _, err := parseService(ref)
	if err != nil {
		return err
	}
	if err := checkNames(drop); err != nil {
		return err
	}
	current, err := serviceNeeds(project, service)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, n := range current {
		have[n] = true
	}
	var missing []string
	for _, n := range drop {
		if !have[n] {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s does not reach %s\n  hint: gg deps ls %s/%s says what it does reach",
			service, strings.Join(missing, " or "), project, service)
	}
	return setNeeds(project, service, mergeNames(current, nil, drop))
}

// checkNames refuses a dependency spelled as a reference. Cross-project edges do
// not exist — a project is the boundary — so `shop/db` here is somebody applying
// the wrong mental model, and being told costs less than a confusing 400.
func checkNames(names []string) error {
	if len(names) == 0 {
		return fmt.Errorf("which service?\n  hint: gg deps add shop/web db cache")
	}
	for _, n := range names {
		if strings.Contains(n, "/") {
			bare := n[strings.LastIndex(n, "/")+1:]
			return fmt.Errorf(
				"%q names a project, and a service can only reach services in its own\n  hint: %s", n, bare)
		}
		if !nameRe.MatchString(n) {
			return fmt.Errorf("%q is not a usable service name: %s", n, nameShape)
		}
	}
	return nil
}

// mergeNames is the set arithmetic both verbs do before sending the result.
func mergeNames(current, add, drop []string) []string {
	set := map[string]bool{}
	for _, n := range current {
		set[n] = true
	}
	for _, n := range add {
		set[n] = true
	}
	for _, n := range drop {
		delete(set, n)
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
