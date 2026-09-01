// Command gg is the gagarin CLI.
//
// It is a thin wrapper over the API and holds no state of its own beyond an
// access token. Anything `gg` can do, the API can do — there is no second path.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/pflag"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "gg: %v\n", err)
		os.Exit(1)
	}
}

// ---- API plumbing -------------------------------------------------------

// defaultAPI is where gg looks when nothing says otherwise. It is a constant
// rather than configuration because an agent that has never seen gagarin should
// be able to reach it without being told an address.
const defaultAPI = "https://api.gagarin.cloud"

func apiBase() string {
	if v := os.Getenv("GAGARIN_API"); v != "" {
		return strings.TrimRight(v, "/")
	}
	if creds, err := loadCredentials(); err == nil && creds.API != "" {
		return strings.TrimRight(creds.API, "/")
	}
	return defaultAPI
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"fix_hint"`
}

func (e apiError) Error() string {
	s := fmt.Sprintf("[%s] %s", e.Code, e.Message)
	if e.Hint != "" {
		s += "\n  hint: " + e.Hint
	}
	return s
}

func call(method, path string, body any, out any) error {
	base, token, err := resolveAuth()
	if err != nil {
		return err
	}
	return callTo(base, token, method, path, body, out)
}

// callAnon talks to the onboarding endpoints, which have no credential yet.
func callAnon(method, path string, body any, out any) error {
	return callTo(apiBase(), "", method, path, body, out)
}

func callTo(base, token, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, base+path, rdr)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	// Ask for JSON explicitly: the onboarding endpoints answer in prose by default,
	// because their usual reader is a language model rather than a program.
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", clientName())

	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach control plane at %s: %w", base, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		var wrap struct{ Error apiError }
		if json.Unmarshal(raw, &wrap) == nil && wrap.Error.Code != "" {
			return wrap.Error
		}
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// callYAML fetches something that is a file rather than a response — `gg eject`
// is the only such endpoint. Errors still arrive in the JSON envelope, so the
// failure path is shared with call() and only the success path differs.
func callYAML(method, path string) (string, error) {
	base, token, err := resolveAuth()
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(method, base+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/yaml")
	req.Header.Set("User-Agent", clientName())

	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return "", fmt.Errorf("cannot reach control plane at %s: %w", base, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		var wrap struct{ Error apiError }
		if json.Unmarshal(raw, &wrap) == nil && wrap.Error.Code != "" {
			return "", wrap.Error
		}
		return "", fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	return string(raw), nil
}

// ---- commands -----------------------------------------------------------

func cmdInit(name string) error {
	name, err := parseProject(name)
	if err != nil {
		return err
	}
	var p project
	if err := call("POST", "/v1/projects", map[string]string{"name": name}, &p); err != nil {
		return err
	}
	// The id is printed because it is not cosmetic: image paths and hostnames are
	// built from it, so a human reading this output has seen the thing they will
	// meet again in a URL.
	fmt.Printf("project %s created (id %s)\n", p.Name, p.ID)
	return nil
}

// project is what the control plane calls a project. The name is the label a
// human chose and is unique only within one account; the id is generated, global,
// and what everything addressable is built from.
type project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

func cmdProjects() error {
	var out struct {
		Projects []project `json:"projects"`
	}
	if err := call("GET", "/v1/projects", nil, &out); err != nil {
		return err
	}
	if len(out.Projects) == 0 {
		fmt.Println("no projects yet; `gg init` creates one")
		return nil
	}
	// The id is not cosmetic: names are unique only within one account, so a
	// project somebody shared can carry the same name as your own — the id is
	// what tells them apart, and what image paths and hostnames are built from.
	for _, p := range out.Projects {
		role := p.Role
		if role == "owner" {
			role = "owner (pays for it)"
		}
		fmt.Printf("%-30s  %-10s  %s\n", p.Name, p.ID, role)
	}
	return nil
}

// resolveProject turns whatever the user typed — a name or an id — into the
// project itself. The API accepts either in a URL, but a push has to happen
// before anything is registered, and the registry path needs the id.
func resolveProject(ref string) (project, error) {
	var p project
	err := call("GET", "/v1/projects/"+ref, nil, &p)
	return p, err
}

type deployFlags struct {
	env map[string]string
	// A directory that survives a restart, and how big it may get. Set once, when
	// the service is created; gagarin refuses to change it on a later deploy,
	// because moving a volume abandons the data at the old path and resizing one
	// can destroy a filesystem.
	volumePath   string
	volumeSizeGB int
}

// What is deliberately not here any more: --project, --name, --port, --needs and
// --public.
//
// The first three moved into the service reference, where they are visible in
// the command rather than derived from the directory it was run in. --needs
// moved out of a deploy entirely, to `gg deps` — it is a standing declaration
// about who may talk to whom, and it was replaced wholesale on every deploy, so
// a redeploy that failed to restate a dependency withdrew it. That does not
// break the caller; an undeclared call is dropped rather than refused, so it
// hangs. A capability you can lose by forgetting to mention it is the failure
// nobody sees.
//
// --public left last and for the sharpest version of the same reason. It handed
// out an address on the internet, and an address is not part of the artifact — it
// is a name people have, in a browser, a webhook, a colleague's bookmark. Carried
// on a deploy it could be withdrawn by being forgotten, and from outside that
// looks exactly like an outage nobody caused. `gg domain add` gives a service an
// address now, the same verb that attaches a name you own, and a deploy cannot
// touch either.
//
// --env stayed, and is still replaced wholesale, because env is genuinely part
// of the artifact: it is what this revision ran with, it is what a rollback
// restores, and `gg history` would mean less without it.

// deployFlagVars holds the pflag.FlagSet destinations that need post-processing
// once parsing is done: env vars and env files both feed the same map, and
// precedence must not depend on the order they appeared in.
type deployFlagVars struct {
	f        *deployFlags
	env      []string
	envFiles []string
}

// bindDeployFlags registers the flags that describe a deployed service, on both
// `gg deploy` and `gg ship`, so the two cannot drift.
func bindDeployFlags(fs *pflag.FlagSet) *deployFlagVars {
	v := &deployFlagVars{f: &deployFlags{env: map[string]string{}}}
	fs.StringArrayVar(&v.env, "env", nil, "set an env var K=V (repeatable)")
	fs.StringArrayVar(&v.envFiles, "env-file", nil, "read KEY=VALUE lines from a file (repeatable; later\nfiles win, --env flags win over all files)")
	fs.StringVar(&v.f.volumePath, "volume", "", "keep this directory across restarts, e.g.\n/var/lib/postgresql/data. Set once, at the first\ndeploy; a later deploy cannot move or resize it")
	fs.IntVar(&v.f.volumeSizeGB, "volume-size", 0, "how big the volume may get, in GB (default 10)")
	return v
}

// finish applies env-file/-env precedence once flags have been parsed.
func (v *deployFlagVars) finish() (*deployFlags, error) {
	for _, path := range v.envFiles {
		vals, err := parseEnvFile(path)
		if err != nil {
			return nil, err
		}
		for k, val := range vals {
			v.f.env[k] = val
		}
	}
	for _, kv := range v.env {
		k, val, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("--env expects K=V, got %q", kv)
		}
		v.f.env[k] = val
	}
	return v.f, nil
}

// parseDeploy parses the deploy flags on their own, outside of cobra. It exists
// for tests: it defines the identical flag set newDeployCmd binds to its
// cobra.Command.
func parseDeploy(args []string) (*deployFlags, error) {
	fs := pflag.NewFlagSet("deploy", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	v := bindDeployFlags(fs)
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return v.finish()
}

// defaultPort is what a service listens on when the reference does not say.
//
// Kept, and kept at the conventional number, because most containers do listen
// here and restating it on every redeploy is noise. It is printed on every
// deploy rather than left implicit, so a service that is answering on the wrong
// port is visible in the output instead of only in a timeout.
const defaultPort = 8080

// cmdDeploy runs an image that is already in the project's registry space.
//
// It does one thing. It does not build, it does not push, and it does not touch
// the dependency graph, the domain or the volume of a service that already
// exists — all of those are declared elsewhere and outlive any one image.
func cmdDeploy(ref, image string, f *deployFlags) error {
	project, service, port, err := parseService(ref)
	if err != nil {
		return err
	}
	repo, tag, err := parseRepoTag(image, project)
	if err != nil {
		return err
	}
	target, err := resolveTarget(project, repo, tag)
	if err != nil {
		return err
	}
	return deployImage(project, service, port, target, f)
}

// deployImage is the registration itself, shared with `gg ship`.
func deployImage(project, service string, port int, t *registryTarget, f *deployFlags) error {
	if port == 0 {
		port = defaultPort
	}

	// gagarin pins by digest, and the server clears the pin when it is not sent.
	// A deploy no longer follows a push, so there is no push output to read one
	// out of — it is asked of the registry instead. A failure here is not fatal:
	// running by tag is what every other platform does, and refusing to deploy
	// over it would be worse than saying so.
	digest := t.digest
	if digest == "" {
		digest = imageDigest(t.ref)
	}

	fmt.Printf("→ recording %s as %s on port %d\n", t.short(), service, port)
	if digest == "" {
		fmt.Printf("  (no digest for that tag; %s will run by tag rather than pinned)\n", service)
	}

	body := map[string]any{
		"image":  t.ref,
		"digest": digest,
		"port":   port,
		"env":    f.env,
	}
	// Only sent when asked for. An absent volume and a volume of zero size are
	// different requests, and the control plane refuses the second.
	if f.volumePath != "" {
		body["volume_path"] = f.volumePath
		if f.volumeSizeGB > 0 {
			body["volume_size_gb"] = f.volumeSizeGB
		}
	}
	var svc struct {
		Name string `json:"name"`
	}
	path := fmt.Sprintf("/v1/projects/%s/services/%s", project, service)
	if err := call("PUT", path, body, &svc); err != nil {
		return err
	}

	// Submitted, not awaited.
	//
	// Every command here writes a demand and returns. Nothing blocks on the
	// cluster catching up, because the cluster catching up is not this command's
	// business — gagarin reconciles, and `gg status` is where you find out
	// whether it has. That is the same shape a domain already had: declare it,
	// and read who is holding it up.
	//
	// It also removes a claim gg was in no position to make. A deploy used to sit
	// on the status endpoint for ninety seconds and then announce a URL, which
	// read as "it works" — and for a private service it skipped even that and
	// said "is running" on the strength of the write having succeeded. The
	// control plane accepts any image inside the project's own space, existing or
	// not, so a typo deploys cleanly and sits in ImagePullBackOff. Saying nothing
	// about the running state is more honest than saying something unchecked, and
	// it is one fewer place for the answer to disagree with `gg status`.
	fmt.Printf("  %s\n", t.short())
	// Deliberately silent about where the service can be reached.
	//
	// A deploy no longer decides that — it cannot add an address and it cannot
	// take one away — so anything printed here would be this command repeating a
	// fact it did not establish and cannot check. `gg status` holds the addresses,
	// and one place saying it is better than two that can disagree.
	fmt.Printf("\ngagarin is converging on that. `gg status %s` says when it has,\nand where %s answers.\n", project, service)
	return nil
}

type statusResp struct {
	Project   string          `json:"project"`
	ProjectID string          `json:"project_id"`
	Services  []serviceStatus `json:"services"`
	Platform  platformState   `json:"platform"`
	// Notices is everything the reader is meant to be told before they read the
	// table: a suspended project, a reconciler that has stopped. One list
	// rather than a field per kind, because a second channel for "print this
	// first" is a second thing to remember to render, and the one added later
	// is the one that goes unrendered. Empty when there is nothing to say.
	Notices    []string   `json:"notices"`
	UsageToday usageToday `json:"usage_today"`
}

// platformState is what gagarin says about itself, as structured fact. The
// sentence a person reads arrives in Notices instead; this is the boolean
// anything else would branch on.
type platformState struct {
	Converging bool `json:"converging"`
}

// usageToday is today's accrued cost so far, folded server-side from the same
// billing log a real invoice will fold from — see the API's internal/api/usage.go.
// An estimate, not a bill: the minute a service is currently in is still
// running, so this total only grows across a single day.
//
// Micro-dollars — millionths — because the tariff is $0.014 per running hour
// and bills per minute, so cents would round a minute of a service away to
// nothing. The server chose the unit for the same reason; this is the same
// integer it sent.
type usageToday struct {
	MicroUSD int64 `json:"micro_usd"`
}

// domainStatus is where a declared domain is in its handshake, and which of the
// two parties is holding it up. Both, because the state alone misleads:
// "waiting" reads as "the platform is working on it", and for half the states
// the platform can do nothing until a DNS record changes.
type domainStatus struct {
	// URL is the address in the form somebody can click; Domain is the bare
	// hostname, which is what a registrar and a certificate are about.
	URL    string `json:"url"`
	Domain string `json:"domain"`
	// Generated marks the address gagarin handed out, as against a name its owner
	// brought. Only one of the two can ever be waiting on somebody's registrar.
	Generated bool              `json:"generated"`
	State     string            `json:"state"`
	BlockedOn string            `json:"blocked_on"`
	Sentence  string            `json:"sentence"`
	DNS       map[string]string `json:"dns"`
}

// Named rather than anonymous so the renderers in status.go can take one.
type serviceStatus struct {
	// Kind distinguishes a service from a resource. Absent from an older control
	// plane, which had only services.
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Image string `json:"image"`
	Port  int    `json:"port"`
	// What this service is allowed to reach. Server-side truth: it is the same
	// list that decides whether the packets arrive, not a description of it.
	Needs        []string `json:"needs"`
	VolumePath   string   `json:"volume_path"`
	VolumeSizeGB int      `json:"volume_size_gb"`
	InSync       bool     `json:"in_sync"`
	// Domains is every address this service answers on — a name its owner brought
	// first, the one gagarin handed out last — and empty for a service that is not
	// on the internet.
	//
	// One list rather than a URL and a domain side by side. They are the same kind
	// of fact, and holding them apart is what put one in a column and the other in
	// a sub-row under it, as though a service could be reachable in two different
	// senses. "Public" is not a field either: it is whether this list is empty.
	//
	// Reported separately from InSync on purpose: a domain waiting on somebody's
	// registrar is not a cluster mismatch, and the platform cannot converge it.
	Domains  []domainStatus `json:"domains"`
	Sentence string         `json:"sentence"`
	Actual   struct {
		Exists bool `json:"exists"`
		// Ready counts pods of the revision that was asked for, not every
		// revision at once — so a redeploy whose new pod never starts reads as
		// 0 rather than borrowing the readiness of the pod it is replacing.
		Ready   int32 `json:"ready_replicas"`
		Desired int32 `json:"desired_replicas"`
		// Superseded is how many pods of an older revision are still serving.
		// Non-zero means the service is up but is not the version asked for.
		Superseded int32 `json:"superseded_replicas"`
		// Stalled is Kubernetes' own verdict that this stopped being a rollout
		// in progress and became one that failed. It is what lets a service
		// that is genuinely still starting be told apart from one that is never
		// going to start — a replica count alone cannot do that, and without it
		// every deploy reads as broken for its first few seconds.
		Stalled bool   `json:"stalled"`
		Message string `json:"message"`
	} `json:"actual"`
}

func cmdStatus(ref string) error {
	project, err := parseProject(ref)
	if err != nil {
		return err
	}

	var st statusResp
	if err := call("GET", "/v1/projects/"+project+"/status", nil, &st); err != nil {
		return err
	}
	printStatusTable(st)
	return nil
}

func cmdLogs(ref string) error {
	project, service, _, err := parseService(ref)
	if err != nil {
		return err
	}
	var out struct{ Logs string }
	if err := call("GET", "/v1/projects/"+project+"/services/"+service+"/logs", nil, &out); err != nil {
		return err
	}
	fmt.Print(out.Logs)
	return nil
}

// ---- sharing ------------------------------------------------------------
//
// A project has one owner — the account that pays for it — and any number of
// editors and viewers. You cannot hand over ownership here, because ownership is
// the bill; and for the same reason nobody but the owner can delete a project.

func cmdShare(ref, email, role string) error {
	if role == "" {
		role = "editor"
	}
	project, err := parseProject(ref)
	if err != nil {
		return err
	}

	if err := call("PUT", "/v1/projects/"+project+"/members",
		map[string]string{"email": email, "role": role}, nil); err != nil {
		return err
	}
	what := "can read " + project
	if role == "editor" {
		what = "can deploy to and manage " + project + " (but not delete it)"
	}
	fmt.Printf("%s %s\n", email, what)
	return nil
}

func cmdUnshare(ref, email string) error {
	project, err := parseProject(ref)
	if err != nil {
		return err
	}
	if err := call("DELETE", "/v1/projects/"+project+"/members/"+url.PathEscape(email), nil, nil); err != nil {
		return err
	}
	fmt.Printf("%s can no longer reach %s\n", email, project)
	return nil
}

func cmdMembers(ref string) error {
	project, err := parseProject(ref)
	if err != nil {
		return err
	}
	var out struct {
		Owner   string `json:"owner"`
		Members []struct {
			Email string `json:"email"`
			Role  string `json:"role"`
		} `json:"members"`
	}
	if err := call("GET", "/v1/projects/"+project+"/members", nil, &out); err != nil {
		return err
	}
	fmt.Printf("%-32s owner (pays for it)\n", out.Owner)
	for _, m := range out.Members {
		fmt.Printf("%-32s %s\n", m.Email, m.Role)
	}
	return nil
}

// cmdDestroy destroys a project, or one thing inside it.
//
// Which of the two depends only on the shape of what was typed: `shop` is the
// project, `shop/db` is something in it. There used to be --service and
// --resource flags here, because a database and a container are deleted through
// different endpoints and the caller had to say which they meant — with the
// server refusing a wrong guess, which was better than succeeding but still put
// the guess on the caller.
//
// It is not a guess now. `gg status` already reports what kind each name is, so
// the kind is looked up and the right endpoint called. One extra read before an
// irreversible act is a good trade, and it turns "you called a database a
// service" into a question nobody has to answer.
func cmdDestroy(ref string) error {
	if !strings.Contains(ref, "/") {
		project, err := parseProject(ref)
		if err != nil {
			return err
		}
		if err := call("DELETE", "/v1/projects/"+project, nil, nil); err != nil {
			return err
		}
		fmt.Printf("project %s destroyed\n", project)
		return nil
	}

	project, name, _, err := parseService(ref)
	if err != nil {
		return err
	}
	kind, err := kindOf(project, name)
	if err != nil {
		return err
	}
	if kind == kindResource {
		path := fmt.Sprintf("/v1/projects/%s/resources/%s", project, name)
		if err := call("DELETE", path, nil, nil); err != nil {
			return err
		}
		fmt.Printf("resource %s destroyed, and everything in it\n", name)
		return nil
	}
	path := fmt.Sprintf("/v1/projects/%s/services/%s", project, name)
	if err := call("DELETE", path, nil, nil); err != nil {
		return err
	}
	fmt.Printf("service %s destroyed\n", name)
	return nil
}

// The two things a name in a project can be. A resource is provisioned — a
// database, a cache — and a service is an image somebody built.
const (
	kindService  = "service"
	kindResource = "resource"
)

// kindOf asks the platform what a name is. An older control plane does not
// report a kind at all, and a service is the right thing to assume there: it is
// what the field's absence meant before resources existed.
func kindOf(project, name string) (string, error) {
	var st statusResp
	if err := call("GET", "/v1/projects/"+project+"/status", nil, &st); err != nil {
		return "", err
	}
	for _, s := range st.Services {
		if s.Name != name {
			continue
		}
		if s.Kind != "" && s.Kind != "container" {
			return kindResource, nil
		}
		return kindService, nil
	}
	return "", fmt.Errorf("%s has no service or resource called %s\n  hint: gg status %s lists them", project, name, project)
}

func cmdRegistryLogin() error {
	registry := os.Getenv("GAGARIN_REGISTRY")
	if registry == "" {
		var who struct {
			Registry string `json:"registry"`
		}
		if err := call("GET", "/v1/whoami", nil, &who); err != nil {
			return err
		}
		registry = who.Registry
	}
	if registry == "" {
		return fmt.Errorf("no image registry: the control plane did not report one and GAGARIN_REGISTRY is not set")
	}
	host := strings.SplitN(registry, "/", 2)[0]

	// The gagarin credential IS the registry credential.
	//
	// This used to shell out to `yc iam create-token` and log in as `iam`, which
	// meant pushing required the user's own Yandex Cloud account — so it worked
	// for exactly one person, and not at all for anybody who signed up with an
	// email address. gagarin runs its own registry now and gagarind is its auth
	// realm: docker presents this credential, the token server exchanges it for a
	// short-lived token scoped to the projects this account can reach, and
	// nothing in the path knows what a Yandex is.
	//
	// There used to be a special case here that skipped login for a localhost
	// registry, on the grounds that the dev harness had no accounts in it. It
	// does now — it runs the same registry with the same auth — so the special
	// case described a configuration that no longer exists.
	_, secret, err := resolveAuth()
	if err != nil {
		return err
	}

	// The username is not an identity: gagarin authenticates the password alone.
	// docker requires one anyway, and a constant reads better than a blank.
	cmd := exec.Command("docker", "login", "--username", "gagarin", "--password-stdin", host)
	cmd.Stdin = strings.NewReader(secret)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker login to %s failed: %w\n  hint: if it says the credential is not valid, run `gg auth`", host, err)
	}
	fmt.Printf("docker logged in to %s\n", host)
	return nil
}

// ---- shell helpers ------------------------------------------------------

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func runCapture(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// parseEnvFile reads a plain KEY=VALUE file.
//
// This is deliberately not a manifest and must never become one: the file may
// carry values, never structure. It cannot name services, set ports, or say what
// is public, so it cannot describe a deployment — only supply data to one. There
// is no variable interpolation, because substitution syntax is a mechanism and
// gagarin rations mechanisms.
func parseEnvFile(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("env file %s: %w", path, err)
	}
	out := map[string]string{}
	for n, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s line %d: expected KEY=VALUE, got %q", path, n+1, line)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("%s line %d: empty key", path, n+1)
		}
		if !validEnvKey(key) {
			return nil, fmt.Errorf("%s line %d: %q is not a valid environment variable name", path, n+1, key)
		}
		out[key] = unquote(strings.TrimSpace(val))
	}
	return out, nil
}

var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validEnvKey(k string) bool { return envKeyRe.MatchString(k) }

// unquote strips one layer of matching quotes. Inside double quotes, the usual
// escapes are honoured; inside single quotes the value is taken literally.
func unquote(v string) string {
	if len(v) < 2 {
		return v
	}
	switch {
	case v[0] == '\'' && v[len(v)-1] == '\'':
		return v[1 : len(v)-1]
	case v[0] == '"' && v[len(v)-1] == '"':
		inner := v[1 : len(v)-1]
		inner = strings.ReplaceAll(inner, `\n`, "\n")
		inner = strings.ReplaceAll(inner, `\t`, "\t")
		inner = strings.ReplaceAll(inner, `\"`, `"`)
		return strings.ReplaceAll(inner, `\\`, `\`)
	}
	return v
}

var digestRe = regexp.MustCompile(`digest:\s*(sha256:[0-9a-f]{64})`)

func parseDigest(pushOutput string) string {
	if m := digestRe.FindStringSubmatch(pushOutput); m != nil {
		return m[1]
	}
	return ""
}
