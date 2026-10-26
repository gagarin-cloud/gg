package main

// install.sh and install.ps1, run for real against a fake release that holds
// a real gg. Both scripts are published with every release and are the first
// thing most people run, so they are tested the way they are used: whole,
// from a shell, against the routes GitHub serves.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// buildGG compiles this checkout as release v, the way goreleaser stamps it.
func buildGG(t *testing.T, v string) []byte {
	t.Helper()
	if testing.Short() {
		t.Skip("builds gg; skipped with -short")
	}
	out := filepath.Join(t.TempDir(), "gg")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	build := exec.Command("go", "build", "-o", out, "-ldflags", "-X main.version="+v, ".")
	if msg, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, msg)
	}
	bin, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return bin
}

// runInstaller runs this platform's install script with env added, the way
// its one-liner does: install.sh piped into sh, install.ps1 into iex.
func runInstaller(t *testing.T, env ...string) (stdout, stderr string, err error) {
	t.Helper()
	script, _ := filepath.Abs("install.sh")
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		script, _ = filepath.Abs("install.ps1")
		cmd = exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", "Get-Content -Raw '"+script+"' | Invoke-Expression")
	} else {
		cmd = exec.Command("sh")
		f, ferr := os.Open(script)
		if ferr != nil {
			t.Fatal(ferr)
		}
		defer f.Close()
		cmd.Stdin = f
	}
	cmd.Env = append(os.Environ(), env...)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err = cmd.Run()
	return o.String(), e.String(), err
}

func binName() string {
	if runtime.GOOS == "windows" {
		return "gg.exe"
	}
	return "gg"
}

func TestInstallScriptInstallsTheLatestRelease(t *testing.T) {
	r := serveRelease(t, "v9.9.9", buildGG(t, "9.9.9"))
	dir := filepath.Join(t.TempDir(), "bin")

	stdout, stderr, err := runInstaller(t, "GAGARIN_RELEASES="+r.srv.URL, "GAGARIN_INSTALL_DIR="+dir)
	if err != nil {
		t.Fatalf("install failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	out, err := exec.Command(filepath.Join(dir, binName()), "version").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(out), "gg 9.9.9 ") {
		t.Fatalf("installed gg says %q", out)
	}
	if !strings.Contains(stdout, "next: gg login") {
		t.Fatalf("the installer did not say what to do next:\n%s", stdout)
	}
	if runtime.GOOS != "windows" && !strings.Contains(stdout, dir+" is not on your PATH") {
		t.Fatalf("the installer did not mention PATH:\n%s", stdout)
	}
}

func TestInstallScriptPinsAVersion(t *testing.T) {
	r := serveRelease(t, "v9.9.9", buildGG(t, "9.9.9"))
	// /latest points at a release that does not exist; the pin must win.
	r.latest = "v0.0.1"
	dir := filepath.Join(t.TempDir(), "bin")

	_, stderr, err := runInstaller(t, "GAGARIN_RELEASES="+r.srv.URL, "GAGARIN_INSTALL_DIR="+dir, "GAGARIN_INSTALL_VERSION=9.9.9")
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, binName())); err != nil {
		t.Fatal(err)
	}
}

// The safety property: an archive that does not match the release's checksum
// is never installed. Nothing at all is written to the install directory.
func TestInstallScriptRefusesAnArchiveThatDoesNotMatchItsChecksum(t *testing.T) {
	r := serveRelease(t, "v9.9.9", []byte("placeholder"))
	asset, tampered := releaseArchive(t, "v9.9.9", runtime.GOOS, runtime.GOARCH, []byte("not gg"))
	r.files[asset] = tampered
	dir := filepath.Join(t.TempDir(), "bin")

	stdout, stderr, err := runInstaller(t, "GAGARIN_RELEASES="+r.srv.URL, "GAGARIN_INSTALL_DIR="+dir)
	if err == nil {
		t.Fatalf("installer accepted a tampered archive\nstdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "does not match its checksum") {
		t.Fatalf("stderr does not say why:\n%s", stderr)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("the install directory holds %d entries after a refusal", len(entries))
	}
}

func TestInstallScriptRefusesAnArchiveWithNoChecksum(t *testing.T) {
	r := serveRelease(t, "v9.9.9", []byte("placeholder"))
	r.files["checksums.txt"] = []byte("0000  something_else.tar.gz\n")
	dir := filepath.Join(t.TempDir(), "bin")

	_, stderr, err := runInstaller(t, "GAGARIN_RELEASES="+r.srv.URL, "GAGARIN_INSTALL_DIR="+dir)
	if err == nil || !strings.Contains(stderr, "no entry for") {
		t.Fatalf("err = %v; stderr:\n%s", err, stderr)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("the install directory holds %d entries after a refusal", len(entries))
	}
}

// An installed gg replaces itself through gg upgrade, and `gg version` keeps
// stdout to the one line scripts read while telling the reader it is behind.
func TestInstalledGGVersionAndUpgrade(t *testing.T) {
	r := serveRelease(t, "v9.9.8", buildGG(t, "9.9.8"))
	dir := filepath.Join(t.TempDir(), "bin")
	if _, stderr, err := runInstaller(t, "GAGARIN_RELEASES="+r.srv.URL, "GAGARIN_INSTALL_DIR="+dir); err != nil {
		t.Fatalf("install failed: %v\n%s", err, stderr)
	}

	newer := serveRelease(t, "v9.9.9", buildGG(t, "9.9.9"))
	gg := filepath.Join(dir, binName())
	env := append(os.Environ(), "GAGARIN_RELEASES="+newer.srv.URL, "XDG_CONFIG_HOME="+t.TempDir())

	version := exec.Command(gg, "version")
	version.Env = env
	var o, e bytes.Buffer
	version.Stdout, version.Stderr = &o, &e
	if err := version.Run(); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(o.String()), "\n"); len(lines) != 1 || !strings.HasPrefix(lines[0], "gg 9.9.8 ") {
		t.Fatalf("gg version stdout = %q; want the one version line", o.String())
	}
	if !strings.Contains(e.String(), "gg v9.9.9 is available (you have v9.9.8)") {
		t.Fatalf("gg version stderr = %q; want the update notice", e.String())
	}

	upgrade := exec.Command(gg, "upgrade")
	upgrade.Env = env
	if msg, err := upgrade.CombinedOutput(); err != nil {
		t.Fatalf("gg upgrade: %v\n%s", err, msg)
	}
	after, err := exec.Command(gg, "version").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(after), "gg 9.9.9 ") {
		t.Fatalf("after gg upgrade, gg version says %q", after)
	}
}
