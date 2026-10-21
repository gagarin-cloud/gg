package main

// Asking whether docker can do its part before anything else happens.
//
// gg builds and pushes with the local docker, and used to find out it could not
// only at the moment it tried: after asking the control plane who the caller
// was, after resolving the project, after checking the Dockerfile. What came
// back then was docker's own stderr followed by "docker build failed: exit
// status 1", which a person can usually decode and an agent cannot act on —
// there was no code to branch on and no hint naming what to do. A machine
// without a running daemon is the most common first-deploy stall there is, so
// it gets the same contract as every other refusal: a stable code, a sentence,
// and a hint that names the fix.
//
// Checked first, before any request, so the answer does not depend on the
// network or the credential, and so nothing is half done when it arrives.

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

// The three ways docker can be unavailable, told apart because each has a
// different fix and only one of them is "start it".
const (
	codeDockerNotInstalled = "docker_not_installed"
	codeDockerNotRunning   = "docker_not_running"
	codeDockerPermission   = "docker_permission_denied"
)

// dockerReady says whether docker can do what the command is about to ask of
// it. needDaemon is false for the commands that only drive the docker client —
// `gg registry login` writes a credential, `gg registry copy` talks to two
// registries through buildx — and true for build and push, which need the
// daemon itself.
func dockerReady(needDaemon bool) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return dockerNotInstalled("docker is not on this machine; gg builds and pushes images with it")
	}
	if !needDaemon {
		return nil
	}
	// `docker version` with only the server's field: it prints the version when
	// the daemon answered, and exits non-zero with the daemon's error when it
	// did not. Cheaper than `docker info`, and the same question.
	out, err := runCapture("docker", "version", "--format", "{{.Server.Version}}")
	if err == nil && strings.TrimSpace(out) != "" {
		return nil
	}
	return dockerDaemonError(out)
}

// dockerBuildxReady is the check for `gg registry copy`, which copies between
// registries with buildx and never touches the daemon.
func dockerBuildxReady() error {
	if err := dockerReady(false); err != nil {
		return err
	}
	if _, err := runCapture("docker", "buildx", "version"); err != nil {
		return dockerNotInstalled("docker is here but its buildx plugin is not; gg registry copy uses it to copy an image between registries")
	}
	return nil
}

// daemonAtRe picks the address out of docker's own complaint, so the message
// says which socket did not answer — the one a DOCKER_HOST or a context points
// at is not always the one somebody expects.
var daemonAtRe = regexp.MustCompile(`daemon at (\S+?)\.?\s`)

// dockerDaemonError sorts docker's output about a daemon it could not reach
// into one of the two daemon codes.
func dockerDaemonError(out string) error {
	if strings.Contains(strings.ToLower(out), "permission denied") {
		return apiError{
			Code:    codeDockerPermission,
			Message: "this user cannot reach the docker daemon",
			Hint:    dockerPermissionHint(),
		}
	}
	msg := "docker is installed but its daemon is not answering"
	if m := daemonAtRe.FindStringSubmatch(out + " "); m != nil {
		msg += " (" + m[1] + ")"
	} else if h := os.Getenv("DOCKER_HOST"); h != "" {
		msg += " (" + h + ")"
	}
	return apiError{Code: codeDockerNotRunning, Message: msg, Hint: dockerStartHint()}
}

func dockerNotInstalled(msg string) error {
	return apiError{Code: codeDockerNotInstalled, Message: msg, Hint: dockerInstallHint()}
}

// The hints differ by platform because the fixes do: there is no colima on
// Windows and no Docker Desktop requirement on Linux.

func dockerInstallHint() string {
	var how string
	switch runtime.GOOS {
	case "darwin":
		how = "install Docker Desktop, OrbStack or colima"
	case "linux":
		how = "install Docker Engine"
	default:
		how = "install Docker Desktop"
	}
	return how + ", then run the command again. An image already in the registry can be run without docker: gg deploy"
}

func dockerStartHint() string {
	var how string
	switch runtime.GOOS {
	case "darwin":
		how = "start Docker Desktop, OrbStack or `colima start`"
	case "linux":
		how = "start the docker service (`sudo systemctl start docker`)"
	default:
		how = "start Docker Desktop"
	}
	return how + ", wait until `docker info` succeeds, then run the command again"
}

func dockerPermissionHint() string {
	if runtime.GOOS == "linux" {
		return "add this user to the docker group (`sudo usermod -aG docker $USER`, then log in again), or ask your human to; do not run gg with sudo"
	}
	return "start Docker Desktop as this user, wait until `docker info` succeeds, then run the command again"
}

// pushFailure explains a push that docker refused. It used to say "run `gg
// registry login` first" whatever went wrong, which sent people to
// re-authenticate over a daemon that had stopped or a registry that was down —
// and it said so without showing what docker had printed, because the output
// was captured for the digest and dropped on failure.
func pushFailure(out string, err error) error {
	low := strings.ToLower(out)
	if strings.Contains(low, "cannot connect to the docker daemon") || strings.Contains(low, "is the docker daemon running") {
		return dockerDaemonError(out)
	}
	if strings.Contains(low, "permission denied while trying to connect") {
		return dockerDaemonError(out)
	}
	reason := lastLines(out, 3)
	switch {
	case strings.Contains(low, "unauthorized"), strings.Contains(low, "authentication required"),
		strings.Contains(low, "denied"), strings.Contains(low, "401"), strings.Contains(low, "403"):
		return fmt.Errorf("docker push failed: %w\n%s\n  hint: the registry refused this machine's login; run `gg registry login`, then push again", err, reason)
	default:
		return fmt.Errorf("docker push failed: %w\n%s\n  hint: docker's reason is above; a 502 or a timeout is the registry having trouble — retry, and report it if it persists", err, reason)
	}
}

// lastLines keeps the end of docker's output, where the reason is, indented
// under the error it explains.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}
