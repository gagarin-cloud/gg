package main

// Where credentials live, and how they get there.
//
// The design goal is that an agent never handles a secret: the human approves
// once, the credential lands in a file, and every later command reads it without
// anyone exporting anything. An env var still works for CI, where there is no
// human to click and no home directory worth writing to.
//
// Where CI's credential comes from is `gg creds create`, run once by a
// human on a machine that is already authorised — see credentials_cmd.go. It is
// worth saying here because this file used to be the whole answer, and the
// answer people found in it was the wrong one: XDG_CONFIG_HOME below redirects
// where the file is *stored*, and it was being used as a way to run the
// human-approval flow twice without clobbering a laptop's own credential. That
// works, and it was never meant to be the mechanism for anything.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// credentials is the on-disk file. Deliberately small: a secret, where it came
// from, and what it can do. Anything else would be state the CLI has to keep in
// sync with the control plane, and the CLI is supposed to hold none.
type credentials struct {
	API        string   `json:"api"`
	Credential string   `json:"credential"`
	Account    string   `json:"account,omitempty"`
	Client     string   `json:"client,omitempty"`
	Scopes     []string `json:"scopes,omitempty"`
	ExpiresAt  string   `json:"expires_at,omitempty"`
}

func credentialsPath() (string, error) {
	// XDG first: agents frequently run in containers where HOME is set to
	// something surprising, and honouring the spec makes that configurable.
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot find a home directory to store credentials in: %w", err)
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "gagarin", "credentials.json"), nil
}

func loadCredentials() (*credentials, error) {
	path, err := credentialsPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c credentials
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("%s is not readable as credentials: %w", path, err)
	}
	return &c, nil
}

// saveCredentials writes the file with an owner-only mode, creating the directory
// the same way. It writes to a temporary file and renames, so an interrupted save
// cannot leave a half-written credential behind.
func saveCredentials(c *credentials) (string, error) {
	path, err := credentialsPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	body, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return "", err
	}
	body = append(body, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), ".credentials-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}

// resolveAuth decides what this invocation authenticates with. Environment wins
// over the file so CI can override without touching a home directory, and so a
// developer can borrow another account for one command without logging out.
func resolveAuth() (api string, secret string, err error) {
	api = strings.TrimRight(os.Getenv("GAGARIN_API"), "/")
	secret = os.Getenv("GAGARIN_TOKEN")
	if secret != "" {
		if api == "" {
			api = defaultAPI
		}
		return api, secret, nil
	}

	creds, loadErr := loadCredentials()
	if loadErr != nil {
		if os.IsNotExist(loadErr) {
			return "", "", errNotAuthenticated{}
		}
		return "", "", loadErr
	}
	if api == "" {
		api = strings.TrimRight(creds.API, "/")
	}
	if api == "" {
		api = defaultAPI
	}
	if creds.Credential == "" {
		return "", "", errNotAuthenticated{}
	}
	return api, creds.Credential, nil
}

// errNotAuthenticated is its own type so the message can teach the whole flow.
// This is the error an agent is most likely to meet first, and it is the only
// place the CLI can explain onboarding before it has an account to ask about.
type errNotAuthenticated struct{}

func (errNotAuthenticated) Error() string {
	return "this machine has no gagarin credentials\n" +
		"  to get some: gg signup <your human's email>\n" +
		"  then run: gg auth --claim <code it prints>\n" +
		"  a single click in that email creates the account and authorises this machine"
}
