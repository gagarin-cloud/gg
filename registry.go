package main

// The `gg registry` command group: getting docker logged in, and getting images
// that somebody else built into a project's own space.

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
)

func cmdRegistry(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("gg registry needs a subcommand: login, copy")
	}
	switch args[0] {
	case "login":
		return cmdRegistryLogin()
	case "copy":
		return cmdRegistryCopy(args[1:])
	default:
		return fmt.Errorf("unknown registry subcommand %q: try login or copy", args[0])
	}
}

// repoRe is the shape a repository name may take inside a project's space. It
// mirrors the check the control plane applies before it will run an image
// (internal/api, checkImage): exactly one path segment, so nothing can climb out
// of the project it belongs to. Applied here as well so a bad name is refused
// while the user is still looking at it, rather than after a long upload.
var repoRe = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)

func cmdRegistryCopy(args []string) error {
	fs := flag.NewFlagSet("registry copy", flag.ContinueOnError)
	project := fs.String("project", defaultName(), "project to copy the image into")
	name := fs.String("name", "", "repository to copy it to (default: the image's own name)")
	// Flags may come before or after the image. Go's flag package stops at the
	// first non-flag argument, so `copy postgres:17 -project x` would silently
	// ignore the project — and that is exactly how a person, or an agent, writes
	// it. Parse in rounds instead, setting the positional aside each time.
	var positional []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(positional) == 0 {
		return fmt.Errorf("which image? e.g. gg registry copy postgres:17-alpine")
	}
	if len(positional) > 1 {
		return fmt.Errorf("one image at a time; got %v", positional)
	}
	source := positional[0]

	var who struct {
		Registry string `json:"registry"`
		Platform string `json:"platform"`
	}
	if err := call("GET", "/v1/whoami", nil, &who); err != nil {
		return err
	}
	registry := os.Getenv("GAGARIN_REGISTRY")
	if registry == "" {
		registry = who.Registry
	}
	if registry == "" {
		return fmt.Errorf("no image registry: the control plane did not report one and GAGARIN_REGISTRY is not set")
	}

	repo, tag, err := splitSource(source, *name)
	if err != nil {
		return err
	}

	p, err := resolveProject(*project)
	if err != nil {
		return err
	}
	target := fmt.Sprintf("%s/%s/%s:%s", strings.TrimRight(registry, "/"), p.ID, repo, tag)

	// Registry to registry, without the image touching this machine.
	//
	// The obvious implementation — pull, tag, push — is wrong in a way that is
	// quiet and expensive. `docker pull --platform` does not re-resolve a tag that
	// is already present locally, so `docker tag` then picks up whatever
	// architecture happened to be cached; an Apple Silicon laptop pushes arm64
	// into a cluster of amd64 nodes and the pod dies much later with an exec
	// format error that says nothing about architecture. Observed, not imagined:
	// the first version of this command did exactly that.
	//
	// Copying the manifest instead sidesteps the question rather than answering
	// it. Every architecture the source published comes across, so the kubelet
	// picks the right one at pull time and no platform has to be guessed here. It
	// is also far faster, because the layers never make the round trip.
	fmt.Printf("→ copying %s\n", source)
	if err := run("docker", "buildx", "imagetools", "create", "--tag", target, source); err != nil {
		return fmt.Errorf("copy failed: %w\n  hint: run `gg registry login` first", err)
	}

	fmt.Printf("\n  %s\n", target)
	if d := imageDigest(target); d != "" {
		fmt.Printf("  %s\n", d)
	}
	fmt.Printf("\nDeploy it with:\n  gg deploy -project %s -name %s -port <port>\n", *project, repo)
	return nil
}

// splitSource works out what to call the copy, given what it came from.
//
// A source may carry a registry host and any number of path segments —
// `ghcr.io/org/tool:v1` — while the target has room for exactly one, because the
// segment before it is the project id and that is the tenant boundary. Where the
// answer is obvious the name is taken; where it is not, the user is asked rather
// than guessed at, because a flattening rule is one more thing to learn and to
// be surprised by.
func splitSource(source, override string) (repo, tag string, err error) {
	ref := source
	tag = "latest"
	// A digest pins harder than a tag, and Docker will not let one be re-tagged
	// onto a name without also naming a tag, so say so rather than produce
	// something confusing.
	if strings.Contains(ref, "@") {
		return "", "", fmt.Errorf("copy an image by tag rather than by digest: %s", source)
	}
	// The last colon is only a tag if it is after the last slash — otherwise it
	// is a port, as in localhost:5000/thing.
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref, tag = ref[:i], ref[i+1:]
	}

	if override != "" {
		repo = override
	} else {
		parts := strings.Split(ref, "/")
		last := parts[len(parts)-1]
		switch {
		// A bare name, or a well-known single-owner path like library/postgres:
		// the last segment is the name anybody would call it.
		case len(parts) <= 2:
			repo = last
		default:
			return "", "", fmt.Errorf(
				"%q has more path segments than a project's space allows; say what to call it: gg registry copy %s -name %s",
				ref, source, last)
		}
	}

	if !repoRe.MatchString(repo) {
		return "", "", fmt.Errorf(
			"%q is not a usable repository name: lowercase letters, digits, and . _ - between them\n  give one with -name",
			repo)
	}
	if !tagRe.MatchString(tag) {
		return "", "", fmt.Errorf("%q is not a usable tag", tag)
	}
	return repo, tag, nil
}

// tagRe is docker's own tag shape, restated so a bad one is caught before a pull.
var tagRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)

// imageDigest asks the registry what the copy landed as. gagarin runs images by
// digest, so the useful thing to print is the one the platform will pin.
func imageDigest(ref string) string {
	out, err := runCapture("docker", "buildx", "imagetools", "inspect", ref,
		"--format", "{{.Manifest.Digest}}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
