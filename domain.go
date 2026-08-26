package main

// Custom domains.
//
// A domain is declared, not deployed. It is a claim on a name coordinated with a
// registrar gagarin does not operate, and it outlives any particular image —
// which is why it has its own verb and why `gg deploy` cannot touch it. A flag
// on deploy could release a name by being forgotten, and that failure is
// invisible from the owner's side: their DNS still points here, their
// configuration still looks correct, and the site is simply dark.
//
// The second half of the job is theirs. So the only thing this prints, beyond
// confirming the claim, is the exact record to create — "point DNS at us" is not
// an instruction anybody can act on.

import (
	"fmt"
	"strings"
)

func cmdDomainAdd(domain, service, project string) error {
	if project == "" {
		project = defaultName()
	}
	var out struct {
		Service string            `json:"service"`
		Domain  string            `json:"domain"`
		URL     string            `json:"url"`
		DNS     map[string]string `json:"dns"`
	}
	path := fmt.Sprintf("/v1/projects/%s/services/%s/domain", project, service)
	if err := call("PUT", path, map[string]any{"domain": domain}, &out); err != nil {
		return err
	}

	fmt.Printf("%s answers on %s\n", out.Service, out.Domain)

	// The record, formatted the way a registrar's form asks for it rather than
	// the way our API returns it.
	if out.DNS["type"] != "" {
		fmt.Printf("\nCreate this record where %s is registered:\n\n", registrable(out.Domain))
		fmt.Printf("  type   %s\n", out.DNS["type"])
		fmt.Printf("  name   %s\n", out.DNS["name"])
		fmt.Printf("  value  %s\n", out.DNS["value"])
		if r := out.DNS["reason"]; r != "" {
			// Only present for an apex, and worth printing: somebody who
			// expected a CNAME and got an A record will otherwise assume the
			// answer is wrong.
			fmt.Printf("\n  (%s)\n", r)
		}
	}

	// What happens next, in the order it happens, so nobody waits on the wrong
	// thing. The certificate cannot be ordered before the record exists — see
	// the HTTP-01 note in the engine — so "no HTTPS yet" is the normal first
	// state rather than a fault.
	fmt.Printf(`
Until that record exists, %s will not resolve and no certificate can be
issued — Let's Encrypt has to reach the domain to prove you control it.

  gg status        where it is: waiting on you, or on us
`, out.Domain)
	if out.URL != "" {
		fmt.Printf("\n%s keeps working throughout.\n", out.URL)
	}
	return nil
}

func cmdDomainRemove(domain, service, project string) error {
	if project == "" {
		project = defaultName()
	}
	path := fmt.Sprintf("/v1/projects/%s/services/%s/domain", project, service)
	if err := call("DELETE", path, nil, nil); err != nil {
		return err
	}
	fmt.Printf("%s no longer answers on %s\n", service, domain)
	// Said because it is the thing they will forget, and because a record left
	// pointing at us is a name we are refusing to serve — a visitor gets a
	// stranger's 404 rather than an error that explains itself.
	fmt.Printf("\nRemove the DNS record too; it now points somewhere that will not answer for it.\n")
	return nil
}

// cmdDomainList shows every declared domain in a project and where it is in the
// handshake. It reads the same status the rest of the CLI reads — there is no
// second source for this.
func cmdDomainList(project string) error {
	if project == "" {
		project = defaultName()
	}
	var out struct {
		Services []struct {
			Name   string `json:"name"`
			Domain *domainStatus `json:"domain"`
		} `json:"services"`
	}
	if err := call("GET", "/v1/projects/"+project+"/status", nil, &out); err != nil {
		return err
	}

	found := false
	for _, sv := range out.Services {
		if sv.Domain == nil {
			continue
		}
		found = true
		fmt.Printf("%-34s %-10s %s\n", sv.Domain.Domain, sv.Name, describeDomainState(sv.Domain.State, sv.Domain.BlockedOn))
		fmt.Printf("  %s\n", sv.Domain.Sentence)
		// The sentence says "the record below", so the record has to be below.
		// It is carried only for the states somebody can act on, which is
		// exactly when it is worth printing.
		if d := sv.Domain.DNS; d["type"] != "" {
			fmt.Printf("    %s  %s  →  %s\n", d["type"], d["name"], d["value"])
		}
	}
	if !found {
		fmt.Printf("no domains in %s\n\n  gg domain add shop.example.com --service web\n", project)
	}
	return nil
}

// describeDomainState turns the state and who is blocked on it into one column.
//
// The two are printed together because separately they mislead: "waiting" alone
// implies the platform is working on it, and for two of the four states the
// platform can do nothing at all until somebody edits a DNS record.
func describeDomainState(state, blockedOn string) string {
	switch state {
	case "ok":
		return "ok"
	case "issuing":
		return "issuing certificate (us)"
	case "dns_elsewhere":
		return "DNS points elsewhere (you)"
	case "waiting_dns":
		return "waiting for DNS (you)"
	default:
		if blockedOn != "" {
			return state + " (" + blockedOn + ")"
		}
		return state
	}
}

// registrable is the domain somebody would have typed into a registrar, used
// only to make the instruction read naturally. Two labels from the right, which
// is wrong for a multi-part suffix like .co.uk and costs nothing when it is —
// the record itself is printed in full above.
func registrable(domain string) string {
	parts := strings.Split(domain, ".")
	if len(parts) <= 2 {
		return domain
	}
	return strings.Join(parts[len(parts)-2:], ".")
}
