package main

// How things are named on the command line.
//
// Everything gagarin runs lives in a project, so everything gg operates on is
// addressed the same way: `project/name`, optionally with a `:suffix` that means
// a port for a service and a tag for an image.
//
//	gg logs      shop/web
//	gg deploy    shop/web:8080  api:v3
//	gg build     shop/api:v3
//	gg status    shop
//
// gg used to derive the project from the current directory's name. It does not
// any more, and this file is the reason it cannot: there is no code path left
// that consults the working directory for a name. That behaviour was convenient
// exactly once — the first deploy out of a directory that happened to be called
// the right thing — and after that it was a command whose target was invisible
// in the command. In a directory called `web` it deployed service `web` into
// project `web` on port 8080, and none of those three words appeared in what
// anybody typed or read back in a shell history.
//
// These parsers validate rather than repair. A name that is not usable is
// refused with the shape it should have had — the old code sanitised silently,
// which turns "My App" into "my-app" and then deploys into a project the user
// did not name and cannot find.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// nameRe is what a project or a service may be called. It restates the control
// plane's own rule (internal/api, nameRe) so a bad name is refused while the
// user is still looking at it, rather than after a build and a push.
//
// It is stricter than DNS on purpose: these become hostname labels, and a label
// that starts with a digit is legal in DNS but confuses enough things that it is
// not worth the argument.
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,29}$`)

// nameShape is the sentence that goes with a refusal from nameRe. One string,
// because a caller who is told a different rule by two commands has been told
// nothing.
const nameShape = "lowercase letters, digits and hyphens, starting with a letter, 2–30 characters"

// parseProject reads a bare project reference: `shop`.
func parseProject(s string) (string, error) {
	if s == "" {
		return "", fmt.Errorf("which project?\n  hint: gg projects lists the ones you can reach")
	}
	if strings.Contains(s, "/") {
		name, _, _ := strings.Cut(s, "/")
		return "", fmt.Errorf("%q names something inside a project; this takes the project itself\n  hint: %s", s, name)
	}
	if !nameRe.MatchString(s) {
		return "", fmt.Errorf("%q is not a usable project name: %s", s, nameShape)
	}
	return s, nil
}

// parseService reads `project/service`, optionally with the port the container
// listens on: `shop/web:8080`.
//
// The port rides along with the name rather than sitting in a flag because it is
// part of what identifies the running thing — "web, on 8080" is how somebody
// describes a service out loud, and a `--port` that could be forgotten on a
// redeploy is one more field with a silent default.
func parseService(s string) (project, name string, port int, err error) {
	project, rest, err := splitRef(s, "service", "shop/web")
	if err != nil {
		return "", "", 0, err
	}
	name, suffix, hadColon := cutSuffix(rest)
	if !nameRe.MatchString(name) {
		return "", "", 0, fmt.Errorf("%q is not a usable service name: %s", name, nameShape)
	}
	if !hadColon {
		return project, name, 0, nil
	}
	port, err = strconv.Atoi(suffix)
	if err != nil || port < 1 || port > 65535 {
		return "", "", 0, fmt.Errorf(
			"%q is not a port: the part after the colon is the TCP port the container listens on, e.g. %s/%s:8080",
			suffix, project, name)
	}
	return project, name, port, nil
}

// parseImage reads `project/repo[:tag]` — an image in a project's own space in
// the gagarin registry. Gagarin runs nothing from anywhere else, so there is
// deliberately no way to spell a reference to Docker Hub here; `gg registry
// copy` is what brings somebody else's image in.
//
// The tag is returned empty when it was not given. Whether that is allowed is
// the caller's business: `gg build` mints one, `gg push` refuses.
func parseImage(s string) (project, repo, tag string, err error) {
	project, rest, err := splitRef(s, "image", "shop/api:v3")
	if err != nil {
		return "", "", "", err
	}
	repo, tag, hadColon := cutSuffix(rest)
	if !repoRe.MatchString(repo) {
		return "", "", "", fmt.Errorf(
			"%q is not a usable repository name: lowercase letters, digits, and . _ - between them", repo)
	}
	if hadColon && !tagRe.MatchString(tag) {
		return "", "", "", fmt.Errorf("%q is not a usable tag", tag)
	}
	return project, repo, tag, nil
}

// splitRef pulls the project off the front of a `project/thing` reference.
//
// The error for a bare name is the one that matters: it is what somebody who
// learned the old CLI, or an agent carrying an old skill, will hit first, and it
// has to teach the shape rather than complain about it.
func splitRef(s, what, example string) (project, rest string, err error) {
	if s == "" {
		return "", "", fmt.Errorf("which %s?\n  hint: %s", what, example)
	}
	project, rest, ok := strings.Cut(s, "/")
	if !ok {
		return "", "", fmt.Errorf(
			"%q does not say which project it is in\n  hint: <project>/%s, e.g. %s", s, s, example)
	}
	if rest == "" {
		return "", "", fmt.Errorf("%q names a project and then nothing\n  hint: %s", s, example)
	}
	if strings.Contains(rest, "/") {
		return "", "", fmt.Errorf(
			"%q has more parts than a %s reference has\n  hint: %s", s, what, example)
	}
	if !nameRe.MatchString(project) {
		return "", "", fmt.Errorf("%q is not a usable project name: %s", project, nameShape)
	}
	return project, rest, nil
}

// cutSuffix splits a trailing `:something` off a name. There are no slashes left
// in what reaches it — splitRef has already refused those — so unlike
// splitSource in registry.go this does not have to tell a tag from a port
// number in a registry host.
//
// It reports whether there was a colon at all, which is not the same question as
// whether the suffix is empty: `shop/web:` is somebody who meant to type a port
// and stopped, and answering it as "no port given" would deploy on the default
// rather than tell them.
func cutSuffix(s string) (name, suffix string, hadColon bool) {
	if i := strings.LastIndex(s, ":"); i >= 0 {
		return s[:i], s[i+1:], true
	}
	return s, "", false
}

// parseRepoTag reads an image reference whose project is already known from
// somewhere else — `gg deploy shop/web api:v3`, where the project came from the
// service. A fully-qualified `shop/api:v3` is accepted too and must agree, so
// that copying a reference out of `gg push`'s output works and a reference to a
// different project is caught rather than silently ignored.
func parseRepoTag(s, project string) (repo, tag string, err error) {
	if strings.Contains(s, "/") {
		named, repo, tag, err := parseImage(s)
		if err != nil {
			return "", "", err
		}
		if named != project {
			return "", "", fmt.Errorf(
				"%q is an image in %s, and this deploys into %s\n  hint: gagarin runs an image only in the project that holds it",
				s, named, project)
		}
		return repo, tag, nil
	}
	repo, tag, hadColon := cutSuffix(s)
	if !repoRe.MatchString(repo) {
		return "", "", fmt.Errorf(
			"%q is not a usable image name: lowercase letters, digits, and . _ - between them\n  hint: gg registry copy brings an image from elsewhere in, and gg build makes one",
			repo)
	}
	if hadColon && !tagRe.MatchString(tag) {
		return "", "", fmt.Errorf("%q is not a usable tag", tag)
	}
	if tag == "" {
		tag = "latest"
	}
	return repo, tag, nil
}
