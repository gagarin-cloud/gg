package main

// The update notice: one line on stderr, at most once a day, when a newer gg
// has been released.
//
// It must never be the reason a command is slow, fails or prints something a
// program did not expect. So it only speaks to a person at a terminal, says
// nothing on stdout, gives up after a second, and treats every failure of its
// own as having nothing to say.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/term"
)

const updateCheckEvery = 24 * time.Hour

// updateCheckTimeout bounds the network check. A variable so tests can shorten it.
var updateCheckTimeout = time.Second

// stderrIsTerminal is the notice's audience test. A variable so tests can be
// the person at a shell.
var stderrIsTerminal = func() bool { return term.IsTerminal(int(os.Stderr.Fd())) }

// updateCheck is what the last check found, kept beside the credential file so
// the network is asked once a day rather than on every command.
type updateCheck struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest,omitempty"`
}

func updateCheckPath() (string, error) {
	creds, err := credentialsPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(creds), "update-check.json"), nil
}

func readUpdateCheck() (updateCheck, bool) {
	var c updateCheck
	p, err := updateCheckPath()
	if err != nil {
		return c, false
	}
	raw, err := os.ReadFile(p)
	if err != nil || json.Unmarshal(raw, &c) != nil {
		return c, false
	}
	return c, true
}

func writeUpdateCheck(c updateCheck) {
	p, err := updateCheckPath()
	if err != nil {
		return
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(p), 0o700) != nil {
		return
	}
	_ = os.WriteFile(p, raw, 0o600)
}

// updateNoticeWanted decides whether this run may print the notice at all.
// args is the command line without the program name.
func updateNoticeWanted(args []string) bool {
	if os.Getenv("GAGARIN_NO_UPDATE_CHECK") != "" || os.Getenv("CI") != "" {
		return false
	}
	if currentTag() == "" || !stderrIsTerminal() {
		return false
	}
	if len(args) > 0 {
		switch args[0] {
		// version and upgrade answer the question themselves; completion runs
		// inside the shell on every tab press and must stay silent.
		case "version", "--version", "upgrade", "completion", "__complete", "__completeNoDesc":
			return false
		}
	}
	return true
}

// startUpdateNotice begins whatever the notice needs before the command runs,
// and returns the function that prints it afterwards. When the last check is
// less than a day old there is no network at all; otherwise the check runs
// alongside the command, and the notice waits for it only for what is left of
// updateCheckTimeout.
func startUpdateNotice(args []string) func(w io.Writer) {
	if !updateNoticeWanted(args) {
		return func(io.Writer) {}
	}
	last, ok := readUpdateCheck()
	if ok && time.Since(last.CheckedAt) < updateCheckEvery {
		return func(w io.Writer) { printUpdateNotice(w, last.Latest) }
	}

	start := time.Now()
	done := make(chan string, 1)
	go func() {
		tag, err := latestTag(updateCheckTimeout)
		if err != nil {
			// Remember the attempt, so a machine that is offline pays for one
			// failed check a day rather than one per command.
			writeUpdateCheck(updateCheck{CheckedAt: time.Now(), Latest: last.Latest})
			done <- ""
			return
		}
		writeUpdateCheck(updateCheck{CheckedAt: time.Now(), Latest: tag})
		done <- tag
	}()
	return func(w io.Writer) {
		wait := updateCheckTimeout - time.Since(start)
		if wait < 0 {
			wait = 0
		}
		select {
		case tag := <-done:
			printUpdateNotice(w, tag)
		case <-time.After(wait):
		}
	}
}

func printUpdateNotice(w io.Writer, latest string) {
	if latest == "" || !newer(latest, currentTag()) {
		return
	}
	fmt.Fprintf(w, "gg %s is available (you have %s). Upgrade: gg upgrade\n", latest, currentTag())
}
