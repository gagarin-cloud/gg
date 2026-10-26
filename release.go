package main

// What gg knows about its own releases: where they live, which is the newest,
// and whether the running binary is behind it. The update notice and
// `gg upgrade` both start here.

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"runtime/debug"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const defaultReleases = "https://github.com/gagarin-cloud/gg/releases"

// releasesBase is where releases are published. GAGARIN_RELEASES overrides it,
// for a mirror or for a test that serves its own; install.sh and install.ps1
// honour the same variable, so one setting moves all three.
func releasesBase() string {
	if v := os.Getenv("GAGARIN_RELEASES"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultReleases
}

// latestTag asks which release is newest. /releases/latest answers with a
// redirect to /releases/tag/<tag>, and reading that redirect rather than the
// GitHub API means no rate limit for a client that has no token to send.
func latestTag(timeout time.Duration) (string, error) {
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequest("HEAD", releasesBase()+"/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "gg/"+currentTag())
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("cannot reach %s: %w", releasesBase(), err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode/100 != 3 || loc == "" {
		return "", fmt.Errorf("%s/latest answered %s, not a redirect to a release", releasesBase(), resp.Status)
	}
	tag := path.Base(loc)
	if !semver.IsValid(tag) {
		return "", fmt.Errorf("%s/latest points at %q, which is not a release version", releasesBase(), tag)
	}
	return tag, nil
}

// currentTag is the running binary's version as a tag ("v0.53.0"), or "" when
// it was built from a checkout and has no version to compare.
func currentTag() string {
	v := version
	if v == "dev" {
		info, ok := debug.ReadBuildInfo()
		if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
			return ""
		}
		v = info.Main.Version
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return ""
	}
	return v
}

// newer reports whether latest is a later release than current. A binary with
// no version is never behind: there is nothing to say it is.
func newer(latest, current string) bool {
	return current != "" && semver.Compare(latest, current) > 0
}

var errNoVersion = errors.New("this gg was built from a checkout and has no version to upgrade from; " +
	"rebuild it, or install a release: https://gagarin.cloud/install.sh")
