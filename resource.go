package main

// Resources: the things a project has, as opposed to the things it runs.
//
// A service is an image you built. A resource is something gagarin provides —
// you name it and say how big, and every other decision is the platform's. That
// is why `add` takes almost no flags: a knob here would be a decision the
// platform should be making.
//
// Credentials are read, never injected. `--needs db` means what it has always
// meant — this service may reach that one — and grants no environment. To
// connect, ask for the credentials and pass them to a deploy like any other
// variable, so a deploy stays the only thing that sets an environment.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

func cmdResourceAdd(typ, name, project string, storageGB int) error {
	if project == "" {
		project = defaultName()
	}
	body := map[string]any{"type": typ}
	// Omitted rather than sent as zero, so the platform's default is the
	// platform's — the same way a deploy omits volume-size.
	if storageGB > 0 {
		body["storage_gb"] = storageGB
	}

	var out struct {
		Sentence string `json:"sentence"`
	}
	path := fmt.Sprintf("/v1/projects/%s/resources/%s", project, name)
	if err := call("PUT", path, body, &out); err != nil {
		return err
	}

	// Waiting is not politeness. The image is a hundred megabytes on first pull,
	// and an agent that deploys a dependent against a database still starting
	// gets a connection error that reads like a bug in gagarin.
	fmt.Printf("→ waiting for %s to come up\n", name)
	if err := waitReady(project, name, 3*time.Minute); err != nil {
		fmt.Printf("\n%s was created but is not ready yet: %v\n", name, err)
		fmt.Printf("  check `gg status`\n")
		return nil
	}

	if out.Sentence != "" {
		fmt.Printf("\n%s\n", out.Sentence)
	}
	// The two things to do next, in order. Deliberately no connection string
	// here: printing one would put a password in every terminal scrollback and
	// agent transcript that ever created a database.
	fmt.Printf(`
Connect something to it:
  gg resource secrets %s                 the credentials, to pass as env
  gg deploy --name api --needs %s        %s can then reach it
`, name, name, "api")
	return nil
}

// cmdResourceSecrets prints what a caller needs to connect, and nothing else.
//
// Two formats because there are two readers. `env` is KEY=VALUE lines, which is
// exactly what `gg deploy --env-file` already consumes, so the whole flow is one
// pipe. `json` is for anything that would rather use jq than parse.
func cmdResourceSecrets(name, project, format string) error {
	if project == "" {
		project = defaultName()
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
