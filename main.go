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
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "auth":
		err = cmdAuth(os.Args[2:])
	case "signup":
		err = cmdSignup(os.Args[2:])
	case "whoami":
		err = cmdWhoami()
	case "init":
		err = cmdInit(os.Args[2:])
	case "deploy":
		err = cmdDeploy(os.Args[2:])
	case "status":
		err = cmdStatus(os.Args[2:])
	case "logs":
		err = cmdLogs(os.Args[2:])
	case "share":
		err = cmdShare(os.Args[2:])
	case "unshare":
		err = cmdUnshare(os.Args[2:])
	case "members":
		err = cmdMembers(os.Args[2:])
	case "destroy":
		err = cmdDestroy(os.Args[2:])
	case "registry-login":
		err = cmdRegistryLogin()
	case "skill":
		err = cmdSkill(os.Args[2:])
	case "version", "--version", "-v":
		err = cmdVersion()
	case "-h", "--help", "help":
		usage()
		return
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "gg: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `gg - gagarin CLI

  gg signup EMAIL                ask for an account; a human approves by email
  gg auth --claim CODE           wait for that approval and store credentials
  gg whoami                      which account this machine acts as
  gg init [project]              create a project (defaults to directory name)
  gg deploy [flags]              build, push, and run the current directory
      -project NAME              project to deploy into (default: directory name)
      -name NAME                 service name (default: directory name)
      -port N                    port the container listens on (default 8080)
      -private                   do not expose a public URL
      -env K=V                   set an env var (repeatable)
      -env-file PATH             read KEY=VALUE lines from a file (repeatable;
                                 later files win, -env flags win over all files)
  gg status [project]            desired vs actual state for every service
  gg logs SERVICE [project]      recent logs
  gg share EMAIL [project]       give somebody access to a project
      -role editor|viewer        editor deploys and manages but cannot delete
                                 the project; viewer reads (default: editor)
  gg unshare EMAIL [project]     take that access away
  gg members [project]           who can reach a project, and as what
  gg destroy [project]           delete the project and everything in it
  gg registry-login              log docker in to the gagarin registry
  gg skill install               install the agent skill (Claude Code) so your
                                 agent knows how to use gagarin
  gg version                     which gg this is

Credentials live in ~/.config/gagarin/credentials.json after "gg auth". There is
nothing to export.

Environment (overrides the file; meant for CI):
  GAGARIN_API      control plane URL (default %s)
  GAGARIN_TOKEN    a credential, for CI where no human can click a link
  GAGARIN_REGISTRY registry prefix, e.g. cr.yandex/crpsduigtkk4qgrcvjgu
`, defaultAPI)
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

// ---- commands -----------------------------------------------------------

func defaultName() string {
	wd, err := os.Getwd()
	if err != nil {
		return "app"
	}
	return sanitize(filepath.Base(wd))
}

var notAllowed = regexp.MustCompile(`[^a-z0-9-]+`)

func sanitize(s string) string {
	s = notAllowed.ReplaceAllString(strings.ToLower(s), "-")
	s = strings.Trim(s, "-")
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		s = "app-" + s
	}
	if len(s) > 30 {
		s = strings.Trim(s[:30], "-")
	}
	return s
}

func cmdInit(args []string) error {
	name := defaultName()
	if len(args) > 0 && args[0] != "" {
		name = args[0]
	}
	var p struct{ Name string }
	if err := call("POST", "/v1/projects", map[string]string{"name": name}, &p); err != nil {
		return err
	}
	fmt.Printf("project %s created\n", p.Name)
	return nil
}

type deployFlags struct {
	project string
	name    string
	port    int
	private bool
	env     map[string]string
}

func parseDeploy(args []string) (*deployFlags, error) {
	f := &deployFlags{port: 8080, env: map[string]string{}}
	// Files are collected and applied first, then -env flags on top, so
	// precedence does not depend on the order the flags happen to appear in.
	var envFiles []string
	envFlags := map[string]string{}

	for i := 0; i < len(args); i++ {
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", args[i])
			}
			i++
			return args[i], nil
		}
		var err error
		var v string
		switch args[i] {
		case "-project", "--project":
			if v, err = next(); err == nil {
				f.project = v
			}
		case "-name", "--name":
			if v, err = next(); err == nil {
				f.name = v
			}
		case "-port", "--port":
			if v, err = next(); err == nil {
				_, err = fmt.Sscanf(v, "%d", &f.port)
			}
		case "-private", "--private":
			f.private = true
		case "-env", "--env":
			if v, err = next(); err == nil {
				k, val, ok := strings.Cut(v, "=")
				if !ok {
					err = fmt.Errorf("-env expects K=V, got %q", v)
				} else {
					envFlags[k] = val
				}
			}
		case "-env-file", "--env-file":
			if v, err = next(); err == nil {
				envFiles = append(envFiles, v)
			}
		default:
			err = fmt.Errorf("unknown flag %q", args[i])
		}
		if err != nil {
			return nil, err
		}
	}

	for _, path := range envFiles {
		vals, err := parseEnvFile(path)
		if err != nil {
			return nil, err
		}
		for k, v := range vals {
			f.env[k] = v
		}
	}
	for k, v := range envFlags {
		f.env[k] = v
	}
	if f.name == "" {
		f.name = defaultName()
	}
	if f.project == "" {
		f.project = defaultName()
	}
	return f, nil
}

func cmdDeploy(args []string) error {
	f, err := parseDeploy(args)
	if err != nil {
		return err
	}
	if _, err := os.Stat("Dockerfile"); err != nil {
		return fmt.Errorf("no Dockerfile in the current directory\n  hint: gagarin deploys images, so the repo needs a Dockerfile it can build")
	}

	// Ask the platform what its nodes run, and where its registry is, instead of
	// assuming either. Building for the wrong architecture succeeds locally and
	// only fails much later at pull time; and a registry the user has to know and
	// export is one more secret-shaped thing standing between an agent and a
	// deploy, when the control plane already knows the answer.
	var who struct {
		Platform string `json:"platform"`
		Registry string `json:"registry"`
	}
	if err := call("GET", "/v1/whoami", nil, &who); err != nil {
		return err
	}
	if who.Platform == "" {
		return fmt.Errorf("control plane did not report a target platform")
	}
	// The environment still wins, so pushing somewhere else stays possible.
	registry := os.Getenv("GAGARIN_REGISTRY")
	if registry == "" {
		registry = who.Registry
	}
	if registry == "" {
		return fmt.Errorf("no image registry: the control plane did not report one and GAGARIN_REGISTRY is not set")
	}

	tag := fmt.Sprintf("%s/%s/%s:%d", strings.TrimRight(registry, "/"), f.project, f.name, time.Now().Unix())

	fmt.Printf("→ building %s for %s\n", tag, who.Platform)
	if err := run("docker", "build", "--platform", who.Platform, "-t", tag, "."); err != nil {
		return fmt.Errorf("docker build failed: %w", err)
	}

	fmt.Printf("→ pushing\n")
	pushOut, err := runCapture("docker", "push", tag)
	if err != nil {
		return fmt.Errorf("docker push failed: %w\n  hint: run `gg registry-login` first", err)
	}
	digest := parseDigest(pushOut)

	fmt.Printf("→ registering service\n")
	body := map[string]any{
		"image":  tag,
		"digest": digest,
		"port":   f.port,
		"public": !f.private,
		"env":    f.env,
	}
	var svc struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	path := fmt.Sprintf("/v1/projects/%s/services/%s", f.project, f.name)
	if err := call("PUT", path, body, &svc); err != nil {
		return err
	}

	if svc.URL == "" {
		fmt.Printf("\n%s is running as a private service (no public URL)\n", svc.Name)
		return nil
	}
	fmt.Printf("→ waiting for %s to come up\n", svc.Name)
	if err := waitReady(f.project, f.name, 90*time.Second); err != nil {
		fmt.Printf("\n%s deployed but not ready yet: %v\n", svc.Name, err)
		fmt.Printf("check `gg logs %s`\n", f.name)
		return nil
	}
	fmt.Printf("\n  %s\n\n", svc.URL)
	return nil
}

type statusResp struct {
	Project  string `json:"project"`
	Services []struct {
		Name     string `json:"name"`
		Image    string `json:"image"`
		Port     int    `json:"port"`
		Public   bool   `json:"public"`
		URL      string `json:"url"`
		InSync   bool   `json:"in_sync"`
		Sentence string `json:"sentence"`
		Actual   struct {
			Exists  bool   `json:"exists"`
			Ready   int32  `json:"ready_replicas"`
			Desired int32  `json:"desired_replicas"`
			Message string `json:"message"`
		} `json:"actual"`
	} `json:"services"`
}

func waitReady(project, service string, d time.Duration) error {
	deadline := time.Now().Add(d)
	var last string
	for time.Now().Before(deadline) {
		var st statusResp
		if err := call("GET", "/v1/projects/"+project+"/status", nil, &st); err != nil {
			return err
		}
		for _, s := range st.Services {
			if s.Name != service {
				continue
			}
			if s.InSync {
				return nil
			}
			if s.Actual.Message != "" {
				last = s.Actual.Message
			}
		}
		time.Sleep(3 * time.Second)
	}
	if last != "" {
		return fmt.Errorf("%s", last)
	}
	return fmt.Errorf("timed out waiting for readiness")
}

func cmdStatus(args []string) error {
	project := defaultName()
	if len(args) > 0 && args[0] != "" {
		project = args[0]
	}
	var st statusResp
	if err := call("GET", "/v1/projects/"+project+"/status", nil, &st); err != nil {
		return err
	}
	fmt.Printf("project %s\n\n", st.Project)
	if len(st.Services) == 0 {
		fmt.Println("  no services yet")
		return nil
	}
	for _, s := range st.Services {
		mark := "!"
		if s.InSync {
			mark = "*"
		}
		fmt.Printf("  %s %s\n", mark, s.Sentence)
		fmt.Printf("      image   %s\n", s.Image)
		fmt.Printf("      cluster %d/%d ready", s.Actual.Ready, s.Actual.Desired)
		if s.Actual.Message != "" {
			fmt.Printf("  (%s)", s.Actual.Message)
		}
		fmt.Println()
	}
	fmt.Println()
	return nil
}

func cmdLogs(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: gg logs SERVICE [project]")
	}
	service := args[0]
	project := defaultName()
	if len(args) > 1 {
		project = args[1]
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

func cmdShare(args []string) error {
	role := "editor"
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "-role" || args[i] == "--role" {
			if i+1 >= len(args) {
				return fmt.Errorf("-role needs a value: editor or viewer")
			}
			role, i = args[i+1], i+1
			continue
		}
		rest = append(rest, args[i])
	}
	if len(rest) < 1 {
		return fmt.Errorf("usage: gg share EMAIL [project] [-role editor|viewer]")
	}
	email, project := rest[0], defaultName()
	if len(rest) > 1 {
		project = rest[1]
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

func cmdUnshare(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: gg unshare EMAIL [project]")
	}
	email, project := args[0], defaultName()
	if len(args) > 1 {
		project = args[1]
	}
	if err := call("DELETE", "/v1/projects/"+project+"/members/"+url.PathEscape(email), nil, nil); err != nil {
		return err
	}
	fmt.Printf("%s can no longer reach %s\n", email, project)
	return nil
}

func cmdMembers(args []string) error {
	project := defaultName()
	if len(args) > 0 && args[0] != "" {
		project = args[0]
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

func cmdDestroy(args []string) error {
	project := defaultName()
	if len(args) > 0 && args[0] != "" {
		project = args[0]
	}
	if err := call("DELETE", "/v1/projects/"+project, nil, nil); err != nil {
		return err
	}
	fmt.Printf("project %s destroyed\n", project)
	return nil
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
	// A local registry needs no login, and docker trusts localhost over plain HTTP
	// already. Saying so and succeeding is better than failing on a missing `yc`:
	// this step is in the instructions an agent follows, and an error here reads as
	// "gagarin is broken" when in fact there is nothing to do.
	if isLocalRegistry(host) {
		fmt.Printf("%s is a local registry; docker needs no login for it\n", host)
		return nil
	}
	tok, err := runCapture("yc", "iam", "create-token")
	if err != nil {
		return fmt.Errorf("could not mint a registry token: %w", err)
	}
	cmd := exec.Command("docker", "login", "--username", "iam", "--password-stdin", host)
	cmd.Stdin = strings.NewReader(strings.TrimSpace(tok))
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	fmt.Printf("docker logged in to %s\n", host)
	return nil
}

// isLocalRegistry reports whether a registry host is one docker already treats as
// insecure-by-default, which is exactly the set that needs no credentials.
func isLocalRegistry(host string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
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
