package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeDocker puts a `docker` on PATH that runs script, and nothing else on
// PATH, so the test decides exactly what docker says. An empty script means no
// docker at all.
func fakeDocker(t *testing.T, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker is a shell script")
	}
	dir := t.TempDir()
	if script != "" {
		body := "#!/bin/sh\n" + script + "\n"
		if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

// What docker really prints, so the sorting is tested against the words it
// uses rather than words we wish it used.
const (
	daemonDownScript = `echo "Cannot connect to the Docker daemon at unix:///home/me/.docker/run/docker.sock. Is the docker daemon running?" >&2; exit 1`
	permissionScript = `echo 'permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock: Get "http://%2Fvar%2Frun%2Fdocker.sock/v1.24/version": dial unix /var/run/docker.sock: connect: permission denied' >&2; exit 1`
	// A client whose daemon is down, but whose buildx plugin answers — the shape
	// of a machine that can log in and copy but cannot build.
	clientOnlyScript = `case "$1" in version) echo "Cannot connect to the Docker daemon at unix:///x.sock. Is the docker daemon running?" >&2; exit 1;; *) exit 0;; esac`
)

// The safety property is the order: a machine that cannot build hears so
// before anything is asked of the control plane. The fake API fails the test
// on any request at all, so a pre-flight that moved after resolveTarget — or
// went away — fails here even if the eventual error happened to be similar.
func TestBuildPushAndShipRefuseBeforeAnyRequestWhenDockerCannotBuild(t *testing.T) {
	modes := []struct {
		name, script, code, hint string
	}{
		{"not installed", "", codeDockerNotInstalled, "install"},
		{"daemon not running", daemonDownScript, codeDockerNotRunning, "docker info"},
		{"permission denied", permissionScript, codeDockerPermission, ""},
	}
	commands := map[string]func() error{
		"ship":  func() error { return cmdShip("shop/web:8080", buildFlags{}, &deployFlags{}) },
		"build": func() error { return cmdBuild("shop/web", buildFlags{}) },
		"push":  func() error { return cmdPush("shop/web:v1") },
	}
	for _, m := range modes {
		for name, run := range commands {
			t.Run(m.name+"/"+name, func(t *testing.T) {
				fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
					t.Errorf("gg asked the control plane %s %s before checking docker", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusTeapot)
				})
				fakeDocker(t, m.script)

				err := run()
				var ae apiError
				if !errors.As(err, &ae) {
					t.Fatalf("want an apiError with code %s, got %v", m.code, err)
				}
				if ae.Code != m.code {
					t.Errorf("code = %s, want %s (%v)", ae.Code, m.code, err)
				}
				if ae.Hint == "" {
					t.Errorf("no hint: an agent is left with nothing to do (%v)", err)
				}
				if m.hint != "" && !strings.Contains(ae.Hint, m.hint) {
					t.Errorf("hint %q does not mention %q", ae.Hint, m.hint)
				}
			})
		}
	}
}

// The daemon's address is in the message, because a DOCKER_HOST or a context
// pointing somewhere unexpected is a common reason it does not answer.
func TestADaemonThatDoesNotAnswerIsNamed(t *testing.T) {
	fakeDocker(t, daemonDownScript)
	err := dockerReady(true)
	if err == nil || !strings.Contains(err.Error(), "unix:///home/me/.docker/run/docker.sock") {
		t.Fatalf("want the socket in the message, got %v", err)
	}
	if strings.Contains(err.Error(), "sock.") {
		t.Errorf("the sentence's full stop was taken as part of the address: %v", err)
	}
}

func TestADaemonThatAnswersIsReady(t *testing.T) {
	fakeDocker(t, `echo 27.3.1`)
	if err := dockerReady(true); err != nil {
		t.Fatalf("a daemon that answered was refused: %v", err)
	}
}

// Login and copy drive the client only, so a stopped daemon must not stand in
// their way — refusing them would be a new stall, not a fix for one.
func TestLoginAndCopyNeedOnlyTheClient(t *testing.T) {
	fakeDocker(t, clientOnlyScript)
	if err := dockerReady(false); err != nil {
		t.Errorf("registry login refused with the daemon down: %v", err)
	}
	if err := dockerBuildxReady(); err != nil {
		t.Errorf("registry copy refused with the daemon down: %v", err)
	}
}

func TestCopyWithoutBuildxSaysSo(t *testing.T) {
	fakeDocker(t, `case "$1" in buildx) echo "docker: 'buildx' is not a docker command." >&2; exit 1;; *) exit 0;; esac`)
	err := dockerBuildxReady()
	var ae apiError
	if !errors.As(err, &ae) || ae.Code != codeDockerNotInstalled || !strings.Contains(ae.Message, "buildx") {
		t.Fatalf("want docker_not_installed naming buildx, got %v", err)
	}
}

func TestRegistryLoginWithoutDockerRefusesBeforeAnyRequest(t *testing.T) {
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("gg asked the control plane %s %s before checking docker", r.Method, r.URL.Path)
	})
	t.Setenv("GAGARIN_REGISTRY", "")
	fakeDocker(t, "")
	var ae apiError
	if err := cmdRegistryLogin(); !errors.As(err, &ae) || ae.Code != codeDockerNotInstalled {
		t.Fatalf("want docker_not_installed, got %v", err)
	}
}

// `gg registry login` is the fix for exactly one kind of failed push, and the
// hint used to offer it for all of them.
func TestThePushHintNamesLoginOnlyWhenTheRegistryRefused(t *testing.T) {
	exit := errors.New("exit status 1")
	for _, c := range []struct {
		name, out string
		login     bool
		code      string
	}{
		{"refused", "The push refers to repository [r.gagarin.cloud/p/web]\nunauthorized: authentication required", true, ""},
		{"registry down", "The push refers to repository [r.gagarin.cloud/p/web]\nreceived unexpected HTTP status: 502 Bad Gateway", false, ""},
		{"daemon stopped", "Cannot connect to the Docker daemon at unix:///x.sock. Is the docker daemon running?", false, codeDockerNotRunning},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := pushFailure(c.out, exit)
			if c.code != "" {
				var ae apiError
				if !errors.As(err, &ae) || ae.Code != c.code {
					t.Fatalf("want %s, got %v", c.code, err)
				}
				return
			}
			if got := strings.Contains(err.Error(), "gg registry login"); got != c.login {
				t.Errorf("mentions registry login = %v, want %v: %v", got, c.login, err)
			}
			last := c.out[strings.LastIndex(c.out, "\n")+1:]
			if !strings.Contains(err.Error(), last) {
				t.Errorf("docker's reason was dropped: %v", err)
			}
		})
	}
}
