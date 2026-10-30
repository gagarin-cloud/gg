package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// gitRepo makes a repository in a temporary directory, isolated from whatever
// git configuration the machine running the tests has — a global signing key or
// a hook would otherwise decide whether the commit succeeds.
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on this machine")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	return dir
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.name=test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false"}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func committed(t *testing.T) string {
	dir := gitRepo(t)
	write(t, filepath.Join(dir, "api", "Dockerfile"), "FROM scratch\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "first")
	return dir
}

// The tag a build gets unasked is the commit's short hash only when that hash
// describes what is being built; every other case falls back to the clock.
func TestCommitTag(t *testing.T) {
	t.Run("clean tree is the short hash", func(t *testing.T) {
		dir := committed(t)
		want := git(t, dir, "rev-parse", "--short", "HEAD")
		if got := commitTag(dir); got != want {
			t.Errorf("commitTag = %q, want %q", got, want)
		}
	})

	t.Run("a context inside the repository uses the repository's commit", func(t *testing.T) {
		dir := committed(t)
		want := git(t, dir, "rev-parse", "--short", "HEAD")
		if got := commitTag(filepath.Join(dir, "api")); got != want {
			t.Errorf("commitTag = %q, want %q", got, want)
		}
	})

	t.Run("a modified file means the clock", func(t *testing.T) {
		dir := committed(t)
		write(t, filepath.Join(dir, "api", "Dockerfile"), "FROM busybox\n")
		if got := commitTag(dir); got != "" {
			t.Errorf("commitTag = %q on a dirty tree, want the clock", got)
		}
	})

	t.Run("an untracked file means the clock", func(t *testing.T) {
		dir := committed(t)
		write(t, filepath.Join(dir, "api", "main.go"), "package main\n")
		if got := commitTag(filepath.Join(dir, "api")); got != "" {
			t.Errorf("commitTag = %q with an untracked file, want the clock", got)
		}
	})

	t.Run("dirty outside the context still means the clock", func(t *testing.T) {
		dir := committed(t)
		write(t, filepath.Join(dir, "README.md"), "hello\n")
		if got := commitTag(filepath.Join(dir, "api")); got != "" {
			t.Errorf("commitTag = %q, want the clock", got)
		}
	})

	t.Run("an ignored file does not count", func(t *testing.T) {
		dir := committed(t)
		write(t, filepath.Join(dir, ".gitignore"), "*.log\n")
		git(t, dir, "add", ".gitignore")
		git(t, dir, "commit", "-q", "-m", "ignore logs")
		write(t, filepath.Join(dir, "debug.log"), "noise\n")
		want := git(t, dir, "rev-parse", "--short", "HEAD")
		if got := commitTag(dir); got != want {
			t.Errorf("commitTag = %q, want %q", got, want)
		}
	})

	t.Run("a repository with no commits means the clock", func(t *testing.T) {
		dir := gitRepo(t)
		if got := commitTag(dir); got != "" {
			t.Errorf("commitTag = %q, want the clock", got)
		}
	})

	t.Run("not a repository means the clock", func(t *testing.T) {
		gitRepo(t) // for the skip and the isolation; the repository is unused
		t.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
		if got := commitTag(t.TempDir()); got != "" {
			t.Errorf("commitTag = %q, want the clock", got)
		}
	})

	t.Run("no git on the PATH means the clock", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if got := commitTag("."); got != "" {
			t.Errorf("commitTag = %q, want the clock", got)
		}
	})
}

// shipHarness stands up everything a build, push and deploy touch — a fake
// docker that records its arguments, a git that records being asked, and a
// control plane that records the deploy — so a test can assert on what each
// was told.
type shipHarness struct {
	dockerLog, gitLog string
	deploy            map[string]any
}

func newShipHarness(t *testing.T) *shipHarness {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker is a shell script")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git on this machine")
	}
	h := &shipHarness{}
	bin := t.TempDir()
	h.dockerLog = filepath.Join(bin, "docker.log")
	h.gitLog = filepath.Join(bin, "git.log")
	write(t, filepath.Join(bin, "docker"), "#!/bin/sh\necho \"$@\" >> "+h.dockerLog+"\n"+
		`case "$1" in version) echo 27.0.0;; push) echo "latest: digest: sha256:`+strings.Repeat("a", 64)+` size: 1";; esac`+"\n")
	// A git that passes through to the real one, so commitTag works as it would,
	// and leaves a line behind each time it is run.
	write(t, filepath.Join(bin, "git"), "#!/bin/sh\necho \"$@\" >> "+h.gitLog+"\nexec "+realGit+" \"$@\"\n")
	for _, f := range []string{"docker", "git"} {
		if err := os.Chmod(filepath.Join(bin, f), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)

	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/whoami":
			fmt.Fprint(w, `{"platform":"linux/amd64","registry":"registry.test"}`)
		case r.URL.Path == "/v1/projects/shop":
			fmt.Fprint(w, `{"id":"p1","name":"shop"}`)
		case r.Method == "PUT" && r.URL.Path == "/v1/projects/shop/services/web":
			if err := json.NewDecoder(r.Body).Decode(&h.deploy); err != nil {
				t.Errorf("deploy body: %v", err)
			}
			fmt.Fprint(w, `{"name":"web"}`)
		default:
			fmt.Fprint(w, `{}`)
		}
	})
	return h
}

// dockerSaid returns the image docker was told to build and, if it was, push.
func (h *shipHarness) dockerSaid(t *testing.T) (built, pushed string) {
	t.Helper()
	out, _ := os.ReadFile(h.dockerLog)
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) > 0 && f[0] == "build":
			for i := range f[:len(f)-1] {
				if f[i] == "-t" {
					built = f[i+1]
				}
			}
		case len(f) == 2 && f[0] == "push":
			pushed = f[1]
		}
	}
	if built == "" {
		t.Fatalf("docker build never ran; docker saw:\n%s", out)
	}
	return built, pushed
}

func (h *shipHarness) gitRan() bool {
	out, _ := os.ReadFile(h.gitLog)
	return len(out) > 0
}

// captureStderr is capture's other half: commitTag explains a clock tag on
// stderr, where it does not mix with what a pipe would take.
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prev := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = prev }()
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	f()
	_ = w.Close()
	return <-done
}

var clockTag = regexp.MustCompile(`^[0-9]{10,}$`)

// End to end through the commands: the tag `gg build` and `gg ship` choose is
// the one docker builds, pushes, and the control plane is asked to run.
func TestBuildAndShipTagWithTheCommit(t *testing.T) {
	const ref = "registry.test/p1/web:"
	cases := []struct {
		name  string
		dirty bool
		// run is the command under test, given the build context.
		run func(ctx string) error
		// want is the tag: "hash", "clock", or a literal.
		want     string
		pushes   bool
		deploys  bool
		asksGit  bool
		explains bool
	}{
		{name: "build, clean tree", run: func(ctx string) error { return cmdBuild("shop/web", buildFlags{context: ctx}) },
			want: "hash", asksGit: true},
		{name: "build, dirty tree", dirty: true, run: func(ctx string) error { return cmdBuild("shop/web", buildFlags{context: ctx}) },
			want: "clock", asksGit: true, explains: true},
		{name: "build --push, clean tree", run: func(ctx string) error { return cmdBuild("shop/web", buildFlags{context: ctx, push: true}) },
			want: "hash", pushes: true, asksGit: true},
		{name: "build with a tag, clean tree", run: func(ctx string) error { return cmdBuild("shop/web:v3", buildFlags{context: ctx}) },
			want: "v3"},
		{name: "build with a tag, dirty tree", dirty: true, run: func(ctx string) error { return cmdBuild("shop/web:v3", buildFlags{context: ctx}) },
			want: "v3"},
		{name: "ship, clean tree", run: func(ctx string) error { return cmdShip("shop/web:8080", buildFlags{context: ctx}, &deployFlags{}) },
			want: "hash", pushes: true, deploys: true, asksGit: true},
		{name: "ship, dirty tree", dirty: true, run: func(ctx string) error { return cmdShip("shop/web:8080", buildFlags{context: ctx}, &deployFlags{}) },
			want: "clock", pushes: true, deploys: true, asksGit: true, explains: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newShipHarness(t)
			dir := committed(t)
			hash := git(t, dir, "rev-parse", "--short", "HEAD")
			if c.dirty {
				write(t, filepath.Join(dir, "api", "new.txt"), "x\n")
			}
			_ = os.Remove(h.gitLog) // the setup's own git calls are not gg's

			var err error
			stderr := captureStderr(t, func() {
				_ = capture(t, func() { err = c.run(filepath.Join(dir, "api")) })
			})
			if err != nil {
				t.Fatalf("command failed: %v", err)
			}

			built, pushed := h.dockerSaid(t)
			if !strings.HasPrefix(built, ref) {
				t.Fatalf("built %q, not under %s", built, ref)
			}
			tag := strings.TrimPrefix(built, ref)
			switch c.want {
			case "hash":
				if tag != hash {
					t.Errorf("tag = %q, want the commit %q", tag, hash)
				}
			case "clock":
				if !clockTag.MatchString(tag) {
					t.Errorf("tag = %q, want a clock tag", tag)
				}
			default:
				if tag != c.want {
					t.Errorf("tag = %q, want %q", tag, c.want)
				}
			}

			if c.pushes && pushed != built {
				t.Errorf("pushed %q, but built %q", pushed, built)
			}
			if !c.pushes && pushed != "" {
				t.Errorf("pushed %q without being asked to", pushed)
			}
			if c.deploys {
				if h.deploy == nil {
					t.Fatal("no deploy reached the control plane")
				}
				if h.deploy["image"] != built {
					t.Errorf("deployed %v, but built %q", h.deploy["image"], built)
				}
				if h.deploy["digest"] != "sha256:"+strings.Repeat("a", 64) {
					t.Errorf("deploy digest = %v, want the one the push reported", h.deploy["digest"])
				}
			}
			if h.gitRan() != c.asksGit {
				t.Errorf("git ran = %v, want %v: a tag given on the command line must not consult git", h.gitRan(), c.asksGit)
			}
			if explained := strings.Contains(stderr, "uncommitted changes"); explained != c.explains {
				t.Errorf("explained the clock tag = %v, want %v; stderr:\n%s", explained, c.explains, stderr)
			}
		})
	}
}

// The other quiet fallback: a build outside any repository is a clock tag and
// says nothing about git, because not using git is an ordinary way to build.
func TestBuildOutsideARepositoryIsQuietlyTheClock(t *testing.T) {
	h := newShipHarness(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
	dir := t.TempDir()
	write(t, filepath.Join(dir, "Dockerfile"), "FROM scratch\n")

	var err error
	stderr := captureStderr(t, func() {
		_ = capture(t, func() { err = cmdBuild("shop/web", buildFlags{context: dir}) })
	})
	if err != nil {
		t.Fatal(err)
	}
	built, _ := h.dockerSaid(t)
	if tag := strings.TrimPrefix(built, "registry.test/p1/web:"); !clockTag.MatchString(tag) {
		t.Errorf("tag = %q, want a clock tag", tag)
	}
	if stderr != "" {
		t.Errorf("a build outside a repository said something on stderr:\n%s", stderr)
	}
}
