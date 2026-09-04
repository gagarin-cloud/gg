package main

// Resources: the things a project has, as opposed to the things it runs.
//
// A service is an image you built. A resource is something gagarin provides —
// you name it and say how big, and every other decision is the platform's. That
// is why `add` takes almost no flags: a knob here would be a decision the
// platform should be making.
//
// Declaring the dependency is what connects something to a resource.
// `gg deps add api db` opens the route *and* puts db's connection variables in
// api's environment — DB_URL and the parts, named after the resource rather than
// the protocol. So connecting is one call, not a deploy carrying credentials
// somebody read out of a second one.
//
// `gg resource secrets` still exists and still prints them, because reading a
// password is a real thing to want: pasting it into a psql on a laptop, checking
// what an application is being handed, pointing a service outside gagarin at it.
// It is no longer a step in connecting one.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// typeExternal is the one resource type gg has to know by name.
//
// Every other type is a string the control plane validates and gg passes
// through — which is the whole reason `gg resource add` needs no case per type.
// This one earns an exception because two things gg does locally depend on it:
// --env is refused for anything else before a request is made, and the output
// after a create says "it publishes X" rather than "gagarin is provisioning it",
// which would be a wait for a pod that does not exist.
const typeExternal = "external"

func cmdResourceAdd(ref, typ, size string, storageGB int, env map[string]string) error {
	project, name, _, err := parseService(ref)
	if err != nil {
		return err
	}
	// An external is the one type the caller supplies values for, and the one
	// the platform runs nothing for. Both halves of that show up below.
	external := typ == typeExternal
	if !external && len(env) > 0 {
		return fmt.Errorf("only an external takes --env or --env-file: %s mints its own credentials\n  hint: gg resource secrets %s/%s reads them", typ, project, name)
	}
	if external && len(env) == 0 {
		return fmt.Errorf("an external with no values publishes nothing\n  hint: gg resource add %s/%s external --env-file .env.%s", project, name, name)
	}
	body := map[string]any{"type": typ}
	// Sent only when there is something to send, so a restatement that means to
	// change nothing else does not clear the bundle: the control plane reads an
	// absent env as "leave it as it is" and an explicit {} as "publish nothing".
	if len(env) > 0 {
		body["env"] = env
	}
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
	// An external is ready when this call returns — there is no pod to pull an
	// image, so the "gagarin is provisioning it" line below would be a wait for
	// nothing, and `gg status` has nothing to report becoming ready.
	//
	// The variable names are printed because they are the whole of what the
	// resource does, and the caller has just typed the un-prefixed halves: they
	// need to see what an application will actually read. Names only — the
	// values are what they just supplied, and echoing a live key back into a
	// terminal is how it reaches a scrollback.
	if external {
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, envPrefix(name)+"_"+k)
		}
		sort.Strings(keys)
		fmt.Printf("\nIt publishes %s\n", strings.Join(keys, ", "))
		fmt.Printf(`
Connect something to it:
  gg deps add %s/<service> %s

That hands the service those variables. It does not restrict egress —
anything in this project can already reach the internet.
`, project, name)
		return nil
	}
	// Not waited for. This used to block for up to three minutes, on the grounds
	// that the first pull is a hundred megabytes and a dependent that connects
	// too early gets an error reading like a bug in gagarin. That reasoning was
	// about the wrong thing: the fix for connecting too early is to look before
	// connecting, which `gg status` is for, and a command that blocks for three
	// minutes is a command an agent will run twice.
	fmt.Printf("\ngagarin is provisioning it. `gg status %s` says when it is ready.\n", project)
	// The one thing to do next. Deliberately no connection string here: printing
	// one would put a password in every terminal scrollback and agent transcript
	// that ever created a database — and it is not needed, because the
	// declaration below is what hands the credentials over.
	//
	// This used to print two commands, a deploy carrying the credentials and a
	// `gg deps add` opening the route, with a note that both were required and
	// failed differently. One of them is now the whole of it.
	fmt.Printf(`
Connect something to it, in one step:
  gg deps add %s/api %s

That opens the route and hands api the connection variables — %s_URL and the
parts. Or declare it on the deploy itself, so the service never starts without
them:
  gg deploy %s/api:8080 api --deps %s
`, project, name, envPrefix(name), project, name)
	return nil
}

// envPrefix is the prefix a resource's variables are published under: its own
// name, upper-cased, with dashes as underscores. A postgres called `orders-db`
// publishes ORDERS_DB_URL.
//
// Duplicated from the control plane rather than asked of it, and only ever to
// print a hint. The rule is three lines and stable, and a round trip to render
// one line of help would be a round trip that can fail. Nothing branches on
// this: what a service actually holds is what `gg deps add` reported and what
// `gg resource secrets` prints, both of which come from the platform.
func envPrefix(name string) string {
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
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
	// The old resource's name is only known when --source named it. Restoring an
	// exact --backup key gives gg no reliable way to say which resource the key
	// came from, and guessing by parsing the key would be a name printed into a
	// recovery runbook that might not be the right one.
	old := source
	if old == "" {
		old = "<old>"
	}
	fmt.Printf(`
The data is back, under a new name. To finish the recovery:
  gg deps add %s/<dependent> %s     # each dependent, which also hands it %s_*
  gg deps rm  %s/<dependent> %s     # and stop it reading the old one
and once everything reads from %s, remove the old resource. That last step
destroys data, so it is the one that asks for human approval.

The variables are named after the resource, so they changed with the name: a
dependent that read %s_URL now reads %s_URL. Anything holding the old spelling
in its own config needs a deploy too.
`, project, name, envPrefix(name), project, old, name, envPrefix(old), envPrefix(name))
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
