package main

// Resources: the things a project has, as opposed to the things it runs.
//
// A service is an image you built. A resource is something gagarin provides —
// you name it and say how big, and every other decision is the platform's. That
// is why `add` takes almost no flags: a knob here would be a decision the
// platform should be making.
//
// Credentials are read, never injected. `gg deps add api db` means what
// `--needs db` used to mean — this service may reach that one — and grants no
// environment. To connect, ask for the credentials and pass them to a deploy
// like any other variable, so a deploy stays the only thing that sets an
// environment.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func cmdResourceAdd(ref, typ, size string, storageGB int) error {
	project, name, _, err := parseService(ref)
	if err != nil {
		return err
	}
	body := map[string]any{"type": typ}
	// Omitted rather than sent as zero, so the platform's default is the
	// platform's — the same way a deploy omits volume-size.
	if storageGB > 0 {
		body["storage_gb"] = storageGB
	}
	// Same rule, and the same word a service uses: a database at `m` is the same
	// decision as a web service at `m`. Empty means unchanged, so restating a
	// resource does not shrink it.
	if size != "" {
		body["size"] = size
	}

	var out struct {
		Sentence string `json:"sentence"`
	}
	path := fmt.Sprintf("/v1/projects/%s/resources/%s", project, name)
	if err := call("PUT", path, body, &out); err != nil {
		return err
	}

	if out.Sentence != "" {
		fmt.Printf("%s\n", out.Sentence)
	}
	// Not waited for. This used to block for up to three minutes, on the grounds
	// that the first pull is a hundred megabytes and a dependent that connects
	// too early gets an error reading like a bug in gagarin. That reasoning was
	// about the wrong thing: the fix for connecting too early is to look before
	// connecting, which `gg status` is for, and a command that blocks for three
	// minutes is a command an agent will run twice.
	fmt.Printf("\ngagarin is provisioning it. `gg status %s` says when it is ready.\n", project)
	// The two things to do next, in order. Deliberately no connection string
	// here: printing one would put a password in every terminal scrollback and
	// agent transcript that ever created a database.
	// The two halves, in order, and both are required. Without the credentials
	// there is nothing to authenticate with; without the dependency the
	// credentials are correct and the connection hangs.
	fmt.Printf(`
Connect something to it, in two steps:
  gg deploy %s/api:8080 api --env-file <(gg resource secrets %s/%s)
  gg deps add %s/api %s

The first passes the credentials. The second opens the route — without it the
connection is not refused, it hangs.
`, project, project, name, project, name)
	return nil
}

// cmdResourceSecrets prints what a caller needs to connect, and nothing else.
//
// Two formats because there are two readers. `env` is KEY=VALUE lines, which is
// exactly what `gg deploy --env-file` already consumes, so the whole flow is one
// pipe. `json` is for anything that would rather use jq than parse.
func cmdResourceSecrets(ref, format string) error {
	project, name, _, err := parseService(ref)
	if err != nil {
		return err
	}
	var out struct {
		Resource string            `json:"resource"`
		Type     string            `json:"type"`
		Env      map[string]string `json:"env"`
	}
	path := fmt.Sprintf("/v1/projects/%s/resources/%s/secrets", project, name)
	if err := call("GET", path, nil, &out); err != nil {
		return err
	}

	switch format {
	case "env", "":
		// Sorted, so two runs of the same command produce the same bytes and a
		// diff of two env files means something.
		keys := make([]string, 0, len(out.Env))
		for k := range out.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		for _, k := range keys {
			fmt.Fprintf(&b, "%s=%s\n", k, out.Env[k])
		}
		fmt.Print(b.String())
	case "json":
		// Indented, because the other reader of this is a person checking what
		// their agent just did.
		raw, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(raw))
	default:
		return fmt.Errorf("unknown format %q\n  hint: env or json", format)
	}
	return nil
}
