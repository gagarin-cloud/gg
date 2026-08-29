package main

// Addresses: every name a service answers on, and the one verb that changes them.
//
// There are two kinds and they are asked for the same way. `gg domain add
// shop/web shop.example.com` claims a name you own; `gg domain add shop/web`
// hands out the one gagarin generates. Both put a service on the internet, so
// both are the same command — this used to be a `--public` flag on the deploy and
// a separate `domain` verb, which made two unrelated ways to say one thing and
// left `gg status` showing them in two unrelated places.
//
// An address is declared, not deployed. It outlives any particular image, and a
// flag on a deploy could release one by being forgotten — a failure invisible from
// the owner's side: their DNS still points here, their configuration still looks
// correct, and the site is simply dark. So `gg ship` cannot touch either kind.
//
// For a custom name the second half of the job is theirs, at a registrar gagarin
// does not operate. So the only thing that half prints, beyond confirming the
// claim, is the exact record to create — "point DNS at us" is not an instruction
// anybody can act on. The generated address has no second half: we hold the
// wildcard, so it works as soon as the cluster catches up.

import (
	"fmt"
	"net/url"
	"strings"
)

// cmdDomainAdd gives a service an address. An empty domain asks for the generated
// one; anything else claims that name.
func cmdDomainAdd(ref, domain string) error {
	project, service, _, err := parseService(ref)
	if err != nil {
		return err
	}
	var out struct {
		Service string            `json:"service"`
		Domain  string            `json:"domain"`
		URL     string            `json:"url"`
		DNS     map[string]string `json:"dns"`
	}
	body := map[string]any{}
	if domain != "" {
		body["domain"] = domain
	}
	path := fmt.Sprintf("/v1/projects/%s/services/%s/domain", project, service)
	if err := call("PUT", path, body, &out); err != nil {
		return err
	}

	// The generated address. Nothing to coordinate and nobody to wait for, so the
	// whole answer is one line — printing the DNS paragraph for a name we already
	// serve would send somebody to their registrar for no reason.
	if domain == "" {
		fmt.Printf("%s answers on %s\n", out.Service, out.URL)
		fmt.Printf("\n  gg status %s     when the cluster has caught up\n", project)
		return nil
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

  gg status %s     where it is: waiting on you, or on us
`, out.Domain, project)
	if out.URL != "" {
		fmt.Printf("\n%s keeps working throughout.\n", out.URL)
	}
	return nil
}

// cmdDomainRemove takes an address away. An empty domain means the generated one,
// which is what makes a service private again.
//
// It warns before it asks, because the engine's approval email arrives at
// somebody else's inbox and the person typing this is the one who can still stop.
// The warning is sharper for a name they own: a generated address simply stops
// answering, while a custom one keeps resolving here out of a zone file we do not
// control, so visitors get an error rather than a moved-on page.
func cmdDomainRemove(ref, domain string) error {
	project, service, _, err := parseService(ref)
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/v1/projects/%s/services/%s/domain", project, service)
	if domain != "" {
		fmt.Printf("Releasing %s from %s.\n", domain, service)
		fmt.Printf("\n  Anything pointing at that name breaks. Your DNS record will still\n")
		fmt.Printf("  resolve here, so visitors get an error until you remove it too.\n\n")
		path += "?domain=" + url.QueryEscape(domain)
	} else {
		fmt.Printf("Taking %s off the internet.\n", service)
		fmt.Printf("\n  Its address stops answering. Anything holding it — a browser tab, a\n")
		fmt.Printf("  webhook, another team's configuration — breaks with it.\n\n")
	}

	if err := call("DELETE", path, nil, nil); err != nil {
		return err
	}
	if domain != "" {
		fmt.Printf("%s no longer answers on %s\n", service, domain)
		// Said because it is the thing they will forget, and because a record left
		// pointing at us is a name we are refusing to serve — a visitor gets a
		// stranger's 404 rather than an error that explains itself.
		fmt.Printf("\nRemove the DNS record too; it now points somewhere that will not answer for it.\n")
		return nil
	}
	fmt.Printf("%s is private — reachable in %s by name, by the services that need it\n", service, project)
	fmt.Printf("\n  gg domain add %s/%s     puts it back\n", project, service)
	return nil
}

// cmdDomainList shows every address in a project and where each one stands. It
// reads the same status the rest of the CLI reads — there is no second source for
// this, which is why the two cannot disagree.
func cmdDomainList(ref string) error {
	project, err := parseProject(ref)
	if err != nil {
		return err
	}
	var out struct {
		Services []struct {
			Name    string         `json:"name"`
			Domains []domainStatus `json:"domains"`
		} `json:"services"`
	}
	if err := call("GET", "/v1/projects/"+project+"/status", nil, &out); err != nil {
		return err
	}

	found := false
	for _, sv := range out.Services {
		for _, d := range sv.Domains {
			found = true
			fmt.Printf("%-34s %-10s %s\n", d.Domain, sv.Name, describeDomainState(d.State, d.BlockedOn))
			fmt.Printf("  %s\n", d.Sentence)
			// The sentence says "the record below", so the record has to be
			// below. It is carried only for the states somebody can act on,
			// which is exactly when it is worth printing.
			if d.DNS["type"] != "" {
				fmt.Printf("    %s  %s  →  %s\n", d.DNS["type"], d.DNS["name"], d.DNS["value"])
			}
		}
	}
	if !found {
		fmt.Printf("nothing in %s is on the internet\n\n", project)
		fmt.Printf("  gg domain add %s/web                     an address from gagarin\n", project)
		fmt.Printf("  gg domain add %s/web shop.example.com    a name you own\n", project)
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
