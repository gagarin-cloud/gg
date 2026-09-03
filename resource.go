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
	"time"
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

// ---- backups ------------------------------------------------------------

// backupObject mirrors the API's shape: the key's basename is a UTC timestamp,
// so the list arrives oldest-first and the last line is the newest.
type backupObject struct {
	Key       string `json:"key"`
	SizeBytes int64  `json:"size_bytes"`
	Stored    string `json:"stored"`
}

func cmdResourceBackups(ref string) error {
	project, name, _, err := parseService(ref)
	if err != nil {
		return err
	}
	var out struct {
		Type    string         `json:"type"`
		Backups []backupObject `json:"backups"`
	}
	path := fmt.Sprintf("/v1/projects/%s/resources/%s/backups", project, name)
	if err := call("GET", path, nil, &out); err != nil {
		return err
	}
	if len(out.Backups) == 0 {
		fmt.Printf("No backups stored yet. The nightly pass takes the first one, or take one now:\n  gg resource backup %s/%s\n", project, name)
		return nil
	}
	// Oldest first, newest last — the same order the keys sort in, so the line
	// nearest the prompt is the one a recovery reaches for.
	for i, b := range out.Backups {
		mark := ""
		if i == len(out.Backups)-1 {
			mark = "   <- newest"
		}
		fmt.Printf("%s  %s%s\n", b.Key, sizeHuman(b.SizeBytes), mark)
	}
	fmt.Printf("\nTo use one, restore it into a NEW resource:\n  gg resource restore %s/<new-name> --source %s\n", project, name)
	return nil
}

func cmdResourceBackup(ref string) error {
	project, name, _, err := parseService(ref)
	if err != nil {
		return err
	}
	var out struct {
		Backup string `json:"backup"`
	}
	path := fmt.Sprintf("/v1/projects/%s/resources/%s/backups", project, name)
	// The slow client: this streams the whole database to the bucket before it
	// answers, and the answer is the key.
	if err := callSlow("POST", path, nil, &out); err != nil {
		return err
	}
	fmt.Printf("backed up: %s\n", out.Backup)
	return nil
}

// cmdResourceRestore creates a NEW resource and fills it from a backup.
//
// Deliberately never an overwrite: the engine refuses to restore into anything
// holding data, so this verb provisions a fresh postgres, waits for it to run,
// and pours the dump in. That is also why no step here asks for approval — the
// one destructive step in a recovery is removing the old resource, which stays
// on `gg resource rm`'s existing human gate.
func cmdResourceRestore(ref, source, backupKey string, size string, storageGB int) error {
	project, name, _, err := parseService(ref)
	if err != nil {
		return err
	}
	if source == "" && backupKey == "" {
		return fmt.Errorf("say what to restore\n  hint: --source <old-resource> for its newest backup, or --backup <key> for an exact one")
	}

	// 1. The new resource. A restatement if it already exists, which is
	// harmless — the engine's empty-database gate is what actually protects.
	body := map[string]any{"type": "postgres"}
	if storageGB > 0 {
		body["storage_gb"] = storageGB
	}
	if size != "" {
		body["size"] = size
	}
	if err := call("PUT", fmt.Sprintf("/v1/projects/%s/resources/%s", project, name), body, nil); err != nil {
		return err
	}
	fmt.Printf("provisioning %s...\n", name)

	// 2. Wait for it to run. A restore needs a live server to pour into, and
	// "wait, then look" is exactly what an agent would otherwise be told to do
	// by hand. Bounded: a resource that has not started in three minutes is
	// not about to, and the status message says why.
	if err := waitForResource(project, name, 3*time.Minute); err != nil {
		return err
	}

	// 3. Pour.
	var out struct {
		Restored string `json:"restored"`
		Next     string `json:"next"`
	}
	rbody := map[string]any{}
	if source != "" {
		rbody["source"] = source
	}
	if backupKey != "" {
		rbody["backup"] = backupKey
	}
	if err := callSlow("POST", fmt.Sprintf("/v1/projects/%s/resources/%s/restore", project, name), rbody, &out); err != nil {
		return err
	}

	fmt.Printf("restored %s into %s/%s\n", out.Restored, project, name)
	fmt.Printf(`
The data is back, under a new name. To finish the recovery:
  gg deploy ... --env-file <(gg resource secrets %s/%s)   # point each dependent here
  gg deps add %s/<dependent> %s
and once everything reads from %s, remove the old resource. That last step
destroys data, so it is the one that asks for human approval.
`, project, name, project, name, name)
	return nil
}

// waitForResource polls project status until the named service reports a ready
// pod, an unrecoverable state, or the deadline.
func waitForResource(project, name string, patience time.Duration) error {
	deadline := time.Now().Add(patience)
	for {
		var st statusResp
		if err := call("GET", "/v1/projects/"+project+"/status", nil, &st); err != nil {
			return err
		}
		for _, s := range st.Services {
			if s.Name != name {
				continue
			}
			if s.Actual.Ready >= 1 {
				return nil
			}
			if s.Actual.Stalled {
				return fmt.Errorf("%s is not going to start: %s", name, s.Actual.Message)
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not start within %s; `gg status %s` says where it is stuck", name, patience, project)
		}
		time.Sleep(3 * time.Second)
	}
}

func sizeHuman(b int64) string {
	switch {
	case b < 1024:
		return fmt.Sprintf("%d B", b)
	case b < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	case b < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
	default:
		return fmt.Sprintf("%.2f GB", float64(b)/(1024*1024*1024))
	}
}
