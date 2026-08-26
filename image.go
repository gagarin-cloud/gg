package main

// Building an image, and getting it into the registry.
//
// These used to be the first two thirds of `gg deploy`, which built the current
// directory, pushed the result, and registered a service, all under one verb.
// That was three jobs wearing one name, and it had two costs.
//
// The visible one: there was no way to run an image you already had. `gg
// registry copy` would bring somebody else's image into a project and then hand
// you a `gg deploy` that could not use it, because deploy always rebuilt from a
// local Dockerfile and always minted its own tag. The documented recipe for
// running a vector database was a command that did not exist.
//
// The quieter one: a build and a deploy fail for unrelated reasons and want to
// happen at unrelated times. CI builds on every commit and ships on some of
// them; a rollback ships without building anything. Fusing them meant the only
// way to express "push this, do not release it" was to not run gg at all.
//
// So: `gg build` makes an image, `gg push` puts it in the registry, `gg deploy`
// runs one that is already there. `gg ship` is all three, for when you do not
// care about any of the intermediate names — which is most of the time, and is
// what the old `gg deploy` actually meant.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// buildFlags is what a build needs beyond the image reference.
type buildFlags struct {
	// context is the directory docker builds from — the one whose files the
	// Dockerfile can COPY. Explicit rather than always ".", so a repository of
	// several services can be shipped from its root.
	context string
	// file is the Dockerfile, defaulting to one in the context directory.
	file string
	push bool
}

// registryTarget is a resolved image reference: everything needed to name an
// image the same way the control plane will, in the one place that knows how.
type registryTarget struct {
	// project is the name the caller typed; projectID is what the registry path
	// is built from. Names are unique only within an account, so a path built
	// from one would collide between tenants — and the control plane refuses an
	// image that is not under this project's id.
	project   string
	projectID string
	repo      string
	tag       string
	// ref is <registry>/<project-id>/<repo>:<tag> — what docker is told and what
	// the cluster pulls.
	ref string
	// digest is what the registry reported, when something has already asked it.
	// Empty means nobody has, and deployImage will look it up.
	digest string
	// platform is what gagarin's own nodes run. Asked for rather than assumed:
	// building for the wrong architecture succeeds locally and fails much later
	// at pull time, with an error that says nothing about architecture.
	platform string
	registry string
}

// resolveTarget turns a project name, a repository and a tag into a full
// registry reference. An empty tag is minted from the clock, which is what a
// build does when the caller does not care what this one is called.
func resolveTarget(project, repo, tag string) (*registryTarget, error) {
	var who struct {
		Platform string `json:"platform"`
		Registry string `json:"registry"`
	}
	if err := call("GET", "/v1/whoami", nil, &who); err != nil {
		return nil, err
	}
	if who.Platform == "" {
		return nil, fmt.Errorf("control plane did not report a target platform")
	}
	// The environment still wins, so pushing somewhere else stays possible.
	registry := os.Getenv("GAGARIN_REGISTRY")
	if registry == "" {
		registry = who.Registry
	}
	if registry == "" {
		return nil, fmt.Errorf("no image registry: the control plane did not report one and GAGARIN_REGISTRY is not set")
	}

	p, err := resolveProject(project)
	if err != nil {
		return nil, err
	}
	if tag == "" {
		tag = strconv.FormatInt(time.Now().Unix(), 10)
	}
	return &registryTarget{
		project:   project,
		projectID: p.ID,
		repo:      repo,
		tag:       tag,
		ref:       fmt.Sprintf("%s/%s/%s:%s", trimSlash(registry), p.ID, repo, tag),
		platform:  who.Platform,
		registry:  registry,
	}, nil
}

// short is how a caller refers to this image on the command line: `repo:tag`,
// without the registry host and project id gg fills in. Printed rather than the
// full ref wherever the next command is being suggested, so what is shown is
// what can be typed.
func (t *registryTarget) short() string { return t.repo + ":" + t.tag }

func cmdBuild(ref string, f buildFlags) error {
	project, repo, tag, err := parseImage(ref)
	if err != nil {
		return err
	}
	target, err := resolveTarget(project, repo, tag)
	if err != nil {
		return err
	}
	if err := buildImage(target, f); err != nil {
		return err
	}
	if f.push {
		_, err := pushImage(target, true)
		return err
	}

	fmt.Printf("\n  %s\n", target.short())
	fmt.Printf("\nPush it:\n  gg push %s/%s\n", target.project, target.short())
	return nil
}

// buildImage is the docker half, shared with `gg ship`.
func buildImage(t *registryTarget, f buildFlags) error {
	dir := f.context
	if dir == "" {
		dir = "."
	}
	dockerfile := f.file
	if dockerfile == "" {
		dockerfile = filepath.Join(dir, "Dockerfile")
	}
	// Checked here rather than left to docker, because docker's own message for
	// this names a path the caller did not type and does not explain that
	// gagarin runs images and therefore needs one.
	if _, err := os.Stat(dockerfile); err != nil {
		return fmt.Errorf("no Dockerfile at %s\n  hint: gagarin runs images, so a service needs a Dockerfile to build one from; say where it is with --file", dockerfile)
	}

	fmt.Printf("→ building %s for %s\n", t.short(), t.platform)
	args := []string{"build", "--platform", t.platform, "-t", t.ref, "-f", dockerfile, dir}
	if err := run("docker", args...); err != nil {
		return fmt.Errorf("docker build failed: %w", err)
	}
	return nil
}

func cmdPush(ref string) error {
	project, repo, tag, err := parseImage(ref)
	if err != nil {
		return err
	}
	// A push names one image, and "whichever one I built last" is not a name.
	// `gg build` mints a tag when it is not given because it is creating the
	// thing; a push moves something that already exists, so guessing here would
	// mean uploading whatever happened to be tagged with the current second.
	if tag == "" {
		return fmt.Errorf("which tag? a push names one image, and gg will not guess\n  hint: gg push %s/%s:<tag>, or gg build --push to do both", project, repo)
	}
	target, err := resolveTarget(project, repo, tag)
	if err != nil {
		return err
	}
	_, err = pushImage(target, true)
	return err
}

// pushImage uploads and returns the digest the registry reported, so a caller
// that is about to deploy does not have to ask again. `next` says whether to
// print the deploy line afterwards — `gg ship` is about to do the deploy itself.
func pushImage(t *registryTarget, next bool) (string, error) {
	fmt.Printf("→ pushing %s\n", t.short())
	out, err := runCapture("docker", "push", t.ref)
	if err != nil {
		return "", fmt.Errorf("docker push failed: %w\n  hint: run `gg registry login` first", err)
	}
	digest := validDigest(parseDigest(out))
	fmt.Printf("\n  %s\n", t.short())
	if digest != "" {
		fmt.Printf("  %s\n", digest)
	}
	if next {
		fmt.Printf("\nRun it:\n  gg deploy %s/<service>:<port> %s\n", t.project, t.short())
	}
	return digest, nil
}

// cmdShip is build, push and deploy under one verb.
//
// It exists because splitting those apart is right for CI and tedious for a
// person: three commands and an invented tag, to do the thing that used to be
// `gg deploy`. The tag is minted and never asked for, since somebody shipping
// does not have a name in mind for this particular image — and it is still
// printed, so what just happened is recoverable from the scrollback.
//
// The repository is the service's own name. One service, one repository, and its
// history of images is where you would look for it.
func cmdShip(ref string, b buildFlags, d *deployFlags) error {
	project, service, port, err := parseService(ref)
	if err != nil {
		return err
	}
	if !repoRe.MatchString(service) {
		return fmt.Errorf("%q cannot be a repository name, so gg cannot pick one for it\n  hint: gg build %s/<repo> and gg deploy %s/%s separately", service, project, project, service)
	}
	target, err := resolveTarget(project, service, "")
	if err != nil {
		return err
	}
	if err := buildImage(target, b); err != nil {
		return err
	}
	// The digest comes back from the push rather than being asked for again: the
	// registry has just told us what it stored, and a second round trip could
	// only disagree with it.
	digest, err := pushImage(target, false)
	if err != nil {
		return err
	}
	target.digest = digest
	fmt.Println()
	return deployImage(project, service, port, target, d)
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
