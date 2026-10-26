package main

// `gg upgrade`: bring this gg up to the latest release, the same way it was
// installed.
//
// A gg that a package manager put there is upgraded by that package manager,
// because otherwise the manager's record and the file on disk disagree and its
// next upgrade undoes ours. Only a gg nobody manages — install.sh, install.ps1,
// a tarball unpacked by hand — is replaced by gg itself, and only with an
// archive whose checksum matches the release's checksums.txt.

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// installMethod is how this gg got onto the machine, and so how it upgrades.
type installMethod struct {
	name string
	// run is the package manager's own upgrade command, which gg runs.
	run []string
	// manual is a command gg prints instead of running, because it needs root.
	manual string
}

// selfManaged is a gg that no package manager owns, which gg replaces itself.
const selfManaged = "install script or release archive"

// detectInstall works out the install method from where the binary lives.
// goInstalled says whether `go install` built it; owner names the system
// package (deb, rpm, apk) that owns the file, if any.
func detectInstall(exe string, goInstalled bool, owner, latest string) installMethod {
	// Backslashes by hand rather than filepath.ToSlash, which leaves them alone
	// anywhere but Windows, and the tests run everywhere.
	p := strings.ToLower(strings.ReplaceAll(exe, `\`, "/"))
	switch {
	case strings.Contains(p, "/caskroom/gg/"):
		return installMethod{name: "Homebrew", run: []string{"brew", "upgrade", "--cask", "gagarin-cloud/tap/gg"}}
	case strings.Contains(p, "/scoop/apps/gg/"):
		return installMethod{name: "Scoop", run: []string{"scoop", "update", "gg"}}
	case strings.Contains(p, "/winget/packages/gagarin.gg"):
		return installMethod{name: "winget", run: []string{"winget", "upgrade", "--id", "Gagarin.gg", "--exact"}}
	case goInstalled:
		// The tag rather than @latest: the module proxy can take a while to
		// notice a new tag, and @latest would quietly install the old one.
		return installMethod{name: "go install", run: []string{"go", "install", "github.com/gagarin-cloud/gg@" + latest}}
	}

	file := fmt.Sprintf("gg_%s_linux_%s.%s", strings.TrimPrefix(latest, "v"), runtime.GOARCH, owner)
	url := releasesBase() + "/download/" + latest + "/" + file
	switch owner {
	case "deb":
		return installMethod{name: "a .deb package", manual: "curl -fsSLO " + url + " && sudo dpkg -i " + file}
	case "rpm":
		return installMethod{name: "an .rpm package", manual: "sudo rpm -U " + url}
	case "apk":
		return installMethod{name: "an .apk package", manual: "curl -fsSLO " + url + " && sudo apk add --allow-untrusted " + file}
	}
	return installMethod{name: selfManaged}
}

// builtByGoInstall is true for a binary `go install` made: the release pipeline
// stamps main.version, `go install` leaves it alone but records the module
// version. A variable so tests can say which kind of binary they are.
var builtByGoInstall = func() bool {
	if version != "dev" {
		return false
	}
	info, ok := debug.ReadBuildInfo()
	return ok && semver.IsValid(info.Main.Version)
}

// packageOwner names the system package that installed exe, or "" if none
// did. A variable so tests do not depend on the machine's package database.
var packageOwner = func(exe string) string {
	if runtime.GOOS != "linux" {
		return ""
	}
	lists, _ := filepath.Glob("/var/lib/dpkg/info/gg*.list")
	for _, l := range lists {
		raw, err := os.ReadFile(l)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if line == exe {
				return "deb"
			}
		}
	}
	if _, err := exec.LookPath("rpm"); err == nil && exec.Command("rpm", "-qf", exe).Run() == nil {
		return "rpm"
	}
	if _, err := exec.LookPath("apk"); err == nil && exec.Command("apk", "info", "--who-owns", exe).Run() == nil {
		return "apk"
	}
	return ""
}

// executablePath is the binary an upgrade replaces. A variable so a test can
// hand it a file of its own rather than the test binary.
var executablePath = func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("cannot tell where this gg is installed: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

func cmdUpgrade(dryRun bool) error {
	current := currentTag()
	if current == "" {
		return errNoVersion
	}
	latest, err := latestTag(30 * time.Second)
	if err != nil {
		return err
	}
	writeUpdateCheck(updateCheck{CheckedAt: time.Now(), Latest: latest})
	if !newer(latest, current) {
		fmt.Printf("gg %s is the latest release\n", current)
		return nil
	}

	exe, err := executablePath()
	if err != nil {
		return err
	}
	m := detectInstall(exe, builtByGoInstall(), packageOwner(exe), latest)
	switch {
	case m.manual != "" && dryRun:
		fmt.Printf("gg %s is out (you have %s). This gg came from %s, which needs root to replace:\n  %s\n", latest, current, m.name, m.manual)
		return nil
	case m.manual != "":
		return fmt.Errorf("gg %s is out; this gg came from %s, and replacing it needs root. Run:\n  %s", latest, m.name, m.manual)
	case m.run != nil:
		line := strings.Join(m.run, " ")
		if dryRun {
			fmt.Printf("gg %s is out (you have %s). Installed with %s, so gg upgrade would run:\n  %s\n", latest, current, m.name, line)
			return nil
		}
		fmt.Printf("upgrading gg %s → %s with %s:\n  %s\n", current, latest, m.name, line)
		c := exec.Command(m.run[0], m.run[1:]...)
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := c.Run(); err != nil {
			return fmt.Errorf("%s failed: %w", line, err)
		}
		return nil
	}

	if dryRun {
		fmt.Printf("gg %s is out (you have %s). gg upgrade would download it from %s, check it against the release's checksums and replace %s\n",
			latest, current, releasesBase(), exe)
		return nil
	}
	fmt.Printf("upgrading gg %s → %s\n", current, latest)
	if err := upgradeBinary(exe, latest); err != nil {
		return err
	}
	fmt.Printf("gg %s installed at %s\n", latest, exe)
	return nil
}

// upgradeBinary replaces exe with the release tag's binary for this platform.
// The archive is checked against the release's checksums.txt before anything
// is written next to exe; on any failure exe is left exactly as it was.
func upgradeBinary(exe, tag string) error {
	ext, bin := "tar.gz", "gg"
	if runtime.GOOS == "windows" {
		ext, bin = "zip", "gg.exe"
	}
	asset := fmt.Sprintf("gg_%s_%s_%s.%s", strings.TrimPrefix(tag, "v"), runtime.GOOS, runtime.GOARCH, ext)
	base := releasesBase() + "/download/" + tag + "/"

	sums, err := download(base+"checksums.txt", 1<<20)
	if err != nil {
		return err
	}
	want := checksumFor(sums, asset)
	if want == "" {
		return fmt.Errorf("checksums.txt for %s has no entry for %s; refusing to install it", tag, asset)
	}
	archive, err := download(base+asset, 256<<20)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("%s does not match its checksum (expected %s, got %s); refusing to install it", asset, want, got)
	}

	data, err := extractBinary(archive, ext, bin)
	if err != nil {
		return fmt.Errorf("%s: %w", asset, err)
	}
	return replaceExecutable(exe, data)
}

func download(url string, limit int64) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "gg/"+currentTag())
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cannot download %s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("cannot download %s: %w", url, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("cannot download %s: larger than any gg release should be", url)
	}
	return data, nil
}

// checksumFor finds asset in a checksums.txt: "<sha256>  <file>" per line.
func checksumFor(sums []byte, asset string) string {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && f[1] == asset {
			return strings.ToLower(f[0])
		}
	}
	return ""
}

func extractBinary(archive []byte, ext, bin string) ([]byte, error) {
	if ext == "zip" {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if path.Base(f.Name) != bin || f.FileInfo().IsDir() {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
		return nil, fmt.Errorf("no %s in the archive", bin)
	}

	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("no %s in the archive", bin)
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && path.Base(h.Name) == bin {
			return io.ReadAll(tr)
		}
	}
}

// replaceExecutable writes data beside exe and renames it into place, so the
// switch is all or nothing. Windows will not replace a running executable but
// will rename one, so there the old binary steps aside to exe.old first, and
// removeReplacedBinary deletes it the next time gg starts.
func replaceExecutable(exe string, data []byte) error {
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".gg-upgrade-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s, where gg is installed: %w", dir, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("cannot write to %s, where gg is installed: %w", dir, err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}

	if runtime.GOOS != "windows" {
		if err := os.Rename(tmp.Name(), exe); err != nil {
			return fmt.Errorf("cannot replace %s: %w", exe, err)
		}
		return nil
	}
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("cannot replace %s: %w", exe, err)
	}
	if err := os.Rename(tmp.Name(), exe); err != nil {
		_ = os.Rename(old, exe)
		return fmt.Errorf("cannot replace %s: %w", exe, err)
	}
	return nil
}

// removeReplacedBinary deletes the gg.exe.old an upgrade on Windows leaves
// behind. Elsewhere there is never one.
func removeReplacedBinary() {
	if runtime.GOOS != "windows" {
		return
	}
	if exe, err := os.Executable(); err == nil {
		_ = os.Remove(exe + ".old")
	}
}
