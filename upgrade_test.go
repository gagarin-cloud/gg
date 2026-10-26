package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// installedGG puts a stand-in gg binary in a directory of its own and makes
// it the binary gg upgrade replaces, so no test can ever touch the test
// binary itself. It reports as a self-managed install unless a test says
// otherwise.
func installedGG(t *testing.T, content string) string {
	t.Helper()
	name := "gg"
	if runtime.GOOS == "windows" {
		name = "gg.exe"
	}
	exe := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(exe, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	prevExe, prevGo, prevOwner := executablePath, builtByGoInstall, packageOwner
	executablePath = func() (string, error) { return exe, nil }
	builtByGoInstall = func() bool { return false }
	packageOwner = func(string) string { return "" }
	t.Cleanup(func() { executablePath, builtByGoInstall, packageOwner = prevExe, prevGo, prevOwner })
	return exe
}

// untouched fails the test unless exe still holds exactly content and nothing
// else was left in its directory — no half-written replacement beside it.
func untouched(t *testing.T, exe, content string) {
	t.Helper()
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte(content)) {
		t.Fatalf("%s was changed to %q", exe, got)
	}
	entries, _ := os.ReadDir(filepath.Dir(exe))
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("the install directory holds %v; want the old binary alone", names)
	}
}

func TestUpgradeReplacesASelfManagedBinary(t *testing.T) {
	asVersion(t, "0.53.0")
	serveRelease(t, "v0.54.0", []byte("gg v0.54.0"))
	exe := installedGG(t, "gg v0.53.0")

	if err := cmdUpgrade(false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "gg v0.54.0" {
		t.Fatalf("after upgrade the binary is %q", got)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(exe); fi.Mode().Perm()&0o111 == 0 {
			t.Fatalf("the new binary is not executable: %v", fi.Mode())
		}
	}
}

// The safety property: an archive that does not match the release's checksum
// is never installed, and the binary that was there survives byte for byte.
func TestUpgradeRefusesAnArchiveThatDoesNotMatchItsChecksum(t *testing.T) {
	asVersion(t, "0.53.0")
	r := serveRelease(t, "v0.54.0", []byte("gg v0.54.0"))
	exe := installedGG(t, "gg v0.53.0")

	asset, tampered := releaseArchive(t, "v0.54.0", runtime.GOOS, runtime.GOARCH, []byte("not gg"))
	r.files[asset] = tampered

	err := cmdUpgrade(false)
	if err == nil || !strings.Contains(err.Error(), "does not match its checksum") {
		t.Fatalf("err = %v; want a checksum refusal", err)
	}
	untouched(t, exe, "gg v0.53.0")
}

func TestUpgradeRefusesAnArchiveWithNoChecksum(t *testing.T) {
	asVersion(t, "0.53.0")
	r := serveRelease(t, "v0.54.0", []byte("gg v0.54.0"))
	exe := installedGG(t, "gg v0.53.0")
	r.files["checksums.txt"] = []byte("0000  gg_0.54.0_plan9_mips.tar.gz\n")

	err := cmdUpgrade(false)
	if err == nil || !strings.Contains(err.Error(), "no entry for") {
		t.Fatalf("err = %v; want a refusal for a missing checksum", err)
	}
	untouched(t, exe, "gg v0.53.0")
}

func TestUpgradeRefusesWhenChecksumsAreMissing(t *testing.T) {
	asVersion(t, "0.53.0")
	r := serveRelease(t, "v0.54.0", []byte("gg v0.54.0"))
	exe := installedGG(t, "gg v0.53.0")
	delete(r.files, "checksums.txt")

	if err := cmdUpgrade(false); err == nil {
		t.Fatal("upgrade went ahead with no checksums to check against")
	}
	untouched(t, exe, "gg v0.53.0")
}

func TestUpgradeDoesNothingWhenCurrentOrAhead(t *testing.T) {
	for _, latest := range []string{"v0.53.0", "v0.52.0"} {
		t.Run(latest, func(t *testing.T) {
			asVersion(t, "0.53.0")
			r := serveRelease(t, latest, []byte("other gg"))
			exe := installedGG(t, "gg v0.53.0")
			before := r.hits.Load()

			if err := cmdUpgrade(false); err != nil {
				t.Fatal(err)
			}
			untouched(t, exe, "gg v0.53.0")
			// One HEAD for /latest; nothing downloaded.
			if n := r.hits.Load() - before; n != 1 {
				t.Fatalf("made %d requests; want only the latest-release check", n)
			}
		})
	}
}

func TestUpgradeDryRunChangesNothing(t *testing.T) {
	asVersion(t, "0.53.0")
	r := serveRelease(t, "v0.54.0", []byte("gg v0.54.0"))
	exe := installedGG(t, "gg v0.53.0")

	if err := cmdUpgrade(true); err != nil {
		t.Fatal(err)
	}
	untouched(t, exe, "gg v0.53.0")
	if n := r.hits.Load(); n != 1 {
		t.Fatalf("a dry run made %d requests; want only the latest-release check", n)
	}
}

func TestUpgradeRefusesABuildWithNoVersion(t *testing.T) {
	asVersion(t, "dev")
	serveRelease(t, "v0.54.0", []byte("gg v0.54.0"))
	exe := installedGG(t, "gg dev")

	if err := cmdUpgrade(false); err != errNoVersion {
		t.Fatalf("err = %v; want errNoVersion", err)
	}
	untouched(t, exe, "gg dev")
}

func TestUpgradeLeavesAPackagedBinaryToRoot(t *testing.T) {
	asVersion(t, "0.53.0")
	serveRelease(t, "v0.54.0", []byte("gg v0.54.0"))
	exe := installedGG(t, "gg v0.53.0")
	packageOwner = func(string) string { return "deb" }

	err := cmdUpgrade(false)
	if err == nil || !strings.Contains(err.Error(), "sudo dpkg -i gg_0.54.0_linux_") {
		t.Fatalf("err = %v; want the dpkg command to run", err)
	}
	untouched(t, exe, "gg v0.53.0")
}

func TestDetectInstall(t *testing.T) {
	t.Setenv("GAGARIN_RELEASES", "https://example.test/releases")
	for _, c := range []struct {
		exe     string
		goBuilt bool
		owner   string
		want    string
	}{
		{"/opt/homebrew/Caskroom/gg/0.53.0/gg", false, "", "brew upgrade --cask gagarin-cloud/tap/gg"},
		{"/usr/local/Caskroom/gg/0.53.0/gg", false, "", "brew upgrade --cask gagarin-cloud/tap/gg"},
		{`C:\Users\a\scoop\apps\gg\current\gg.exe`, false, "", "scoop update gg"},
		{`C:\Users\a\AppData\Local\Microsoft\WinGet\Packages\Gagarin.gg_Microsoft.Winget.Source_8wekyb3d8bbwe\gg.exe`, false, "", "winget upgrade --id Gagarin.gg --exact"},
		{"/home/a/go/bin/gg", true, "", "go install github.com/gagarin-cloud/gg@v0.54.0"},
		{"/usr/bin/gg", false, "deb", "curl -fsSLO https://example.test/releases/download/v0.54.0/gg_0.54.0_linux_" + runtime.GOARCH + ".deb && sudo dpkg -i gg_0.54.0_linux_" + runtime.GOARCH + ".deb"},
		{"/usr/bin/gg", false, "rpm", "sudo rpm -U https://example.test/releases/download/v0.54.0/gg_0.54.0_linux_" + runtime.GOARCH + ".rpm"},
		{"/usr/bin/gg", false, "apk", "curl -fsSLO https://example.test/releases/download/v0.54.0/gg_0.54.0_linux_" + runtime.GOARCH + ".apk && sudo apk add --allow-untrusted gg_0.54.0_linux_" + runtime.GOARCH + ".apk"},
		{"/home/a/.local/bin/gg", false, "", ""},
		{`C:\Users\a\AppData\Local\Programs\gg\gg.exe`, false, "", ""},
	} {
		m := detectInstall(c.exe, c.goBuilt, c.owner, "v0.54.0")
		got := m.manual
		if m.run != nil {
			got = strings.Join(m.run, " ")
		}
		if got != c.want {
			t.Errorf("%s: %q, want %q", c.exe, got, c.want)
		}
		if c.want == "" && m.name != selfManaged {
			t.Errorf("%s: method %q, want self-managed", c.exe, m.name)
		}
	}
}
