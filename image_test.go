package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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

// The hash reaches docker: what `gg build` and `gg ship` tag the image with
// when they are not told, end to end through the commands themselves.
func TestBuildAndShipTagWithTheCommit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker is a shell script")
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git on this machine")
	}
	commands := map[string]func(ctx string) error{
		"build": func(ctx string) error { return cmdBuild("shop/web", buildFlags{context: ctx}) },
		"ship":  func(ctx string) error { return cmdShip("shop/web:8080", buildFlags{context: ctx}, &deployFlags{}) },
	}
	for name, cmd := range commands {
		for _, dirty := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dirty=%v", name, dirty), func(t *testing.T) {
				dir := committed(t)
				hash := git(t, dir, "rev-parse", "--short", "HEAD")
				if dirty {
					write(t, filepath.Join(dir, "api", "new.txt"), "x\n")
				}

				bin := t.TempDir()
				log := filepath.Join(bin, "args")
				script := "#!/bin/sh\necho \"$@\" >> " + log + "\n" +
					`case "$1" in version) echo 27.0.0;; push) echo "latest: digest: sha256:` + strings.Repeat("a", 64) + ` size: 1";; esac` + "\n"
				write(t, filepath.Join(bin, "docker"), script)
				if err := os.Chmod(filepath.Join(bin, "docker"), 0o755); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", bin+string(os.PathListSeparator)+filepath.Dir(gitPath))

				fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/v1/whoami":
						fmt.Fprint(w, `{"platform":"linux/amd64","registry":"registry.test"}`)
					case "/v1/projects/shop":
						fmt.Fprint(w, `{"id":"p1","name":"shop"}`)
					default:
						fmt.Fprint(w, `{}`)
					}
				})

				_ = cmd(filepath.Join(dir, "api")) // ship's deploy half is not what is tested here
				out, err := os.ReadFile(log)
				if err != nil {
					t.Fatal(err)
				}
				var build string
				for _, line := range strings.Split(string(out), "\n") {
					if strings.HasPrefix(line, "build ") {
						build = line
					}
				}
				if build == "" {
					t.Fatalf("docker build never ran; docker saw:\n%s", out)
				}
				withHash := strings.Contains(build, "registry.test/p1/web:"+hash+" ")
				if withHash == dirty {
					t.Errorf("dirty=%v, but docker was told: %s", dirty, build)
				}
			})
		}
	}
}
