package main

// The `gg registry` command group: getting docker logged in, and getting images
// that somebody else built into a project's own space.

import (
	"fmt"
	"regexp"
	"strings"
)

// repoRe is the shape a repository name may take inside a project's space. It
// mirrors the check the control plane applies before it will run an image
// (internal/api, checkImage): exactly one path segment, so nothing can climb out
// of the project it belongs to. Applied here as well so a bad name is refused
// while the user is still looking at it, rather than after a long upload.
var repoRe = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)

// cmdRegistryCopy brings an image somebody else built into a project's space.
//
// Both halves are named. The destination used to be guessed from the source —
// `gg registry copy ghcr.io/org/tool:v1` became `tool` — with a --name flag for
// when the guess would not work and a refusal for when it could not be made. A
// flattening rule is one more thing to learn and to be surprised by, and now
// that every other command names its project explicitly there is nowhere left
// for the guess to hide: say what to call it, and where.
func cmdRegistryCopy(dest, source string) error {
	project, repo, tag, err := parseImage(dest)
	if err != nil {
		return err
	}
	srcTag, err := sourceTag(source)
	if err != nil {
		return err
	}
	// The source's tag when the destination does not give one. Derived from an
	// argument the caller typed rather than from their working directory, which
	// is the distinction that matters: copying `postgres:17-alpine` and getting
	// `17-alpine` is reading what was said, not inferring what was meant.
	if tag == "" {
		tag = srcTag
	}

	target, err := resolveTarget(project, repo, tag)
	if err != nil {
		return err
	}

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
	if err := run("docker", "buildx", "imagetools", "create", "--tag", target.ref, source); err != nil {
		// Deliberately not "run gg registry login first". That was the only hint
		// this gave, and it is wrong for every failure that is not a credential:
		// a 502 from the registry, a source tag that does not exist, a rate limit
		// at the other end. Sending somebody to re-authenticate over a broken
		// upload costs them the one piece of information docker just printed.
		return fmt.Errorf("copy failed: %w\n  hint: docker printed the reason above."+
			"\n        401 or 403 — run `gg registry login`."+
			"\n        404 — check the source tag exists."+
			"\n        502 or a timeout — the registry is having trouble; retry, and report it if it persists", err)
	}

	fmt.Printf("\n  %s\n", target.short())
	if d := imageDigest(target.ref); d != "" {
		fmt.Printf("  %s\n", d)
	}
	fmt.Printf("\nRun it:\n  gg deploy %s/%s:<port> %s\n", project, repo, target.short())
	return nil
}

// sourceTag reads the tag off the image being copied from. That reference can
// carry a registry host and any number of path segments — `ghcr.io/org/tool:v1`
// — so unlike the destination it is not a gagarin reference and is not parsed
// as one.
func sourceTag(source string) (string, error) {
	if source == "" {
		return "", fmt.Errorf("which image?\n  hint: gg registry copy shop/postgres postgres:17-alpine")
	}
	// A digest pins harder than a tag, and Docker will not let one be re-tagged
	// onto a name without also naming a tag, so say so rather than produce
	// something confusing. Copying one platform out of a multi-architecture image
	// produces a different digest anyway, so a source digest cannot be carried
	// across even in principle.
	if strings.Contains(source, "@") {
		return "", fmt.Errorf("copy an image by tag rather than by digest: %s", source)
	}
	// The last colon is only a tag if it is after the last slash — otherwise it
	// is a port, as in localhost:5000/thing.
	if i := strings.LastIndex(source, ":"); i > strings.LastIndex(source, "/") {
		tag := source[i+1:]
		if !tagRe.MatchString(tag) {
			return "", fmt.Errorf("%q is not a usable tag", tag)
		}
		return tag, nil
	}
	return "latest", nil
}

// tagRe is docker's own tag shape, restated so a bad one is caught before a pull.
var tagRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)

// imageDigest asks the registry what an image landed as. gagarin pins by digest,
// so this is what a deploy sends and what the platform will run.
//
// Note the printf, which is load-bearing and looks like it should not be.
// `--format '{{.Manifest.Digest}}'` reads correctly and does the wrong thing:
// buildx prints its whole default block — Name, MediaType, Digest, and every
// manifest in an index — for any template that references .Manifest directly.
// Wrapping the field defeats that and prints the digest alone. infra/README.md
// documents the same trap for the control plane's own image; this function had
// it too, and got away with it for as long as the result was only printed.
//
// The result is checked rather than trusted, because it is about to be sent to
// an API that will refuse it: an empty answer means "run it by tag", and an
// answer that is not a digest means the same thing rather than a failed deploy.
func imageDigest(ref string) string {
	out, err := runCapture("docker", "buildx", "imagetools", "inspect", ref,
		"--format", `{{printf "%s" .Manifest.Digest}}`)
	if err != nil {
		return ""
	}
	return validDigest(strings.TrimSpace(out))
}

// digestShapeRe is what the control plane will accept (internal/api, digestRe).
var digestShapeRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// validDigest passes a digest through, or returns empty for anything that is
// not one. Empty is a meaningful answer here — it means "pin by tag" — so a
// malformed reading degrades to the same behaviour as no reading at all.
func validDigest(s string) string {
	if digestShapeRe.MatchString(s) {
		return s
	}
	return ""
}
