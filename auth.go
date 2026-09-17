package main

// Onboarding, from the CLI's side.
//
// `gg login` exists so a human never has to copy a secret and an agent never has
// to hold one. Since 2026-09-17 it is the OAuth 2.0 device authorization grant
// (RFC 8628): gg asks the control plane for a code, prints a link, and a human
// opens it, signs in with GitHub or Google, and approves this machine by name.
// Then the credential lands in the file, as it always did.
//
// It used to be two runs — `gg login EMAIL` to ask and `gg login --claim CODE`
// to collect — with the human's part happening in an inbox. That went with email
// sign-in; see brain/docs/051 in the gagarin repo.
//
// # Two runs for an agent, one for a person
//
// The reason it was two runs outlived the email. An agent has to tell its human
// what to open before the human can open it, and an agent sees a command's
// output when the command ends — so a `gg login` that blocked for fifteen
// minutes would hold the link where nobody could read it, and most harnesses
// kill a command long before that anyway. Agents are the main users of this
// command, so the shape follows them:
//
//   - With stdout not a terminal, the first run asks for a code, prints the link,
//     writes the pending authorization next to the credential file, and exits 0.
//     The next run finds it and collects instead of asking again, waiting only
//     briefly; if nobody has approved yet it says so, non-zero, and prints the
//     link again so it can be passed on again.
//   - At a terminal, a person is reading along, so it just waits.
//
// The pending file holds a device code, which is worth something only until it
// expires or is approved, and only to whoever can also get a human to approve
// it. It is written owner-only all the same, the same way as the credential.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/term"
)

// stdoutIsTerminal decides whether anybody is reading along. A variable so tests
// can be the person at a shell.
var stdoutIsTerminal = func() bool { return term.IsTerminal(int(os.Stdout.Fd())) }

// resumeWait is how long a run that is not at a terminal waits for an approval
// that has not happened yet before handing control back to the agent. Long
// enough to catch a human who approved as the agent ran the command; short
// enough to fit inside any harness's command timeout. A variable so tests can
// spend less than a minute learning that nobody approved.
var resumeWait = 60 * time.Second

// deviceConfig is gg as an OAuth client: public, first-party, device grant only.
// Client authentication "in params" because gg has no secret to put in a header,
// and the control plane expects client_id in the form.
func deviceConfig(api string) *oauth2.Config {
	return &oauth2.Config{
		ClientID: "gg",
		Scopes:   []string{"deploy"},
		Endpoint: oauth2.Endpoint{
			DeviceAuthURL: api + "/oauth/device_authorization",
			TokenURL:      api + "/oauth/token",
			AuthStyle:     oauth2.AuthStyleInParams,
		},
	}
}

// pendingLogin is a device authorization somebody has been asked to approve and
// nobody has collected yet.
type pendingLogin struct {
	API                     string    `json:"api"`
	DeviceCode              string    `json:"device_code"`
	UserCode                string    `json:"user_code"`
	VerificationURI         string    `json:"verification_uri"`
	VerificationURIComplete string    `json:"verification_uri_complete,omitempty"`
	Interval                int64     `json:"interval"`
	ExpiresAt               time.Time `json:"expires_at"`
	Label                   string    `json:"label"`
}

func (p *pendingLogin) expired() bool {
	return !p.ExpiresAt.IsZero() && !time.Now().Before(p.ExpiresAt)
}

func pendingLoginPath() (string, error) {
	path, err := credentialsPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(path), "login-pending.json"), nil
}

// loadPendingLogin returns nil, and no error, when there is nothing usable: no
// file, or one that cannot be read, which is a reason to ask afresh rather than
// to stop somebody signing in.
func loadPendingLogin() *pendingLogin {
	path, err := pendingLoginPath()
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var p pendingLogin
	if json.Unmarshal(raw, &p) != nil || p.DeviceCode == "" {
		return nil
	}
	return &p
}

func savePendingLogin(p *pendingLogin) error {
	path, err := pendingLoginPath()
	if err != nil {
		return err
	}
	body, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(path, append(body, '\n'))
}

func removePendingLogin() {
	if path, err := pendingLoginPath(); err == nil {
		_ = os.Remove(path)
	}
}

// cmdLogin starts a sign-in, or collects the one already started.
//
// It never stops at "this machine already acts as": the usual reason to run
// `gg login` on a machine that has a credential is that the credential stopped
// working, and a command that refuses to replace it sends the reader off to
// delete a file by hand.
func cmdLogin(fresh bool) error {
	api := apiBase()
	conf := deviceConfig(api)
	tty := stdoutIsTerminal()

	// One client with a timeout for every request the flow makes, so a control
	// plane that accepts a connection and never answers cannot hang gg.
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient,
		&http.Client{Timeout: 30 * time.Second})

	if fresh {
		removePendingLogin()
	}
	p := loadPendingLogin()
	if p != nil && p.API != api {
		// Asked of another control plane; it cannot be collected from this one.
		p = nil
	}
	if p != nil && p.expired() {
		removePendingLogin()
		fmt.Printf("the code from before (%s) expired unapproved; here is a new one\n\n", p.UserCode)
		p = nil
	}

	if p == nil {
		if creds, err := loadCredentials(); err == nil && creds.Credential != "" && creds.Account != "" {
			fmt.Printf("this machine currently acts as %s; approving replaces that credential\n\n", creds.Account)
		}
		label := clientName()
		da, err := conf.DeviceAuth(ctx, oauth2.SetAuthURLParam("label", label))
		if err != nil {
			return loginError(api, err)
		}
		p = &pendingLogin{
			API:                     api,
			DeviceCode:              da.DeviceCode,
			UserCode:                da.UserCode,
			VerificationURI:         da.VerificationURI,
			VerificationURIComplete: da.VerificationURIComplete,
			Interval:                da.Interval,
			ExpiresAt:               da.Expiry,
			Label:                   label,
		}
		if err := savePendingLogin(p); err != nil {
			return fmt.Errorf("could not remember the sign-in request: %w", err)
		}
		printApprovalRequest(p)
		if !tty {
			fmt.Printf("\nwhen your human has approved, run: gg login\n")
			return nil
		}
		fmt.Printf("\nwaiting for approval ...\n")
	} else if tty {
		printApprovalRequest(p)
		fmt.Printf("\nwaiting for approval ...\n")
	}

	// A person at a terminal waits for as long as the code lives. An agent gets
	// its turn back after resumeWait, so it can speak to its human again.
	waitCtx, cancel := ctx, context.CancelFunc(func() {})
	if !tty {
		waitCtx, cancel = context.WithTimeout(ctx, resumeWait)
	}
	defer cancel()

	tok, err := conf.DeviceAccessToken(waitCtx, &oauth2.DeviceAuthResponse{
		DeviceCode: p.DeviceCode,
		Interval:   p.Interval,
		Expiry:     p.ExpiresAt,
	})
	if err != nil {
		var re *oauth2.RetrieveError
		switch {
		case errors.Is(err, context.DeadlineExceeded) && p.expired():
			removePendingLogin()
			return errCodeExpired
		case errors.Is(err, context.DeadlineExceeded):
			printApprovalRequest(p)
			return apiError{Code: "authorization_pending",
				Message: fmt.Sprintf("nobody has approved code %s yet", p.UserCode),
				Hint: "pass the link and code above to your human again; once they say they approved, run gg login\n" +
					"  to throw this code away and ask for a new one: gg login --new"}
		case errors.As(err, &re) && re.ErrorCode != "":
			// The control plane has ruled on this code, one way or another, so
			// there is nothing left to collect. A network failure is different:
			// the code may still be good, and the next run should try it.
			removePendingLogin()
		}
		return loginError(api, err)
	}
	removePendingLogin()
	return storeLogin(api, tok)
}

// printApprovalRequest is what the human is told, and an agent relays it as it
// stands — so it names the link first, the fallback second, and what to check.
func printApprovalRequest(p *pendingLogin) {
	link := p.VerificationURIComplete
	if link == "" {
		link = p.VerificationURI
	}
	fmt.Printf(`this machine is asking for access to gagarin as "%s".

Tell your human to open this link, sign in with GitHub or Google, and approve:

  %s

If the link does not open, go to %s and enter the code %s.
The page shows that code and the name above; both should match.%s
`, p.Label, link, p.VerificationURI, p.UserCode, expiresIn(p.ExpiresAt))
}

// storeLogin saves an issued credential and does what a fresh credential makes
// possible.
func storeLogin(api string, tok *oauth2.Token) error {
	// The token response says what the credential can do and when it lapses,
	// but not whose it is. whoami does, with the credential just issued — and if
	// that fails the credential is still good, so save it and say less.
	var who struct {
		Account string `json:"account"`
		Client  string `json:"client"`
	}
	_ = callTo(api, tok.AccessToken, "GET", "/v1/whoami", nil, &who)

	var scopes []string
	if s, ok := tok.Extra("scope").(string); ok {
		scopes = strings.Fields(s)
	}
	var expires string
	if !tok.Expiry.IsZero() {
		expires = tok.Expiry.UTC().Format(time.RFC3339)
	}
	path, err := saveCredentials(&credentials{
		API:        api,
		Credential: tok.AccessToken,
		Account:    who.Account,
		Client:     who.Client,
		Scopes:     scopes,
		ExpiresAt:  expires,
	})
	if err != nil {
		return fmt.Errorf("approved, but the credential could not be saved: %w", err)
	}

	if who.Account != "" {
		fmt.Printf("\nthis machine now acts as %s\n", who.Account)
	} else {
		fmt.Printf("\napproved\n")
	}
	fmt.Printf("  credential stored in %s\n", path)
	if len(scopes) > 0 {
		fmt.Printf("  it can %s — deleting anything needs a fresh approval\n",
			strings.Join(scopes, ", "))
	}
	if os.Getenv("GAGARIN_TOKEN") != "" {
		fmt.Printf("  note: GAGARIN_TOKEN is set in this environment, and it wins over the file\n")
	}

	// The gagarin credential is also the registry credential, so there is
	// nothing to ask for and no reason to make somebody run a second command
	// before their first push. Best effort: docker may not be installed on this
	// machine, and that is not a failed authorisation — reading logs and status
	// needs no docker at all.
	if err := cmdRegistryLogin(); err != nil {
		fmt.Printf("\n  (docker is not logged in to the registry yet: %v)\n", err)
		fmt.Printf("  run `gg registry login` before your first push\n")
	}

	fmt.Printf("\nnothing to export. try: gg projects\n")
	return nil
}

// expiresIn is the sentence about how long the code lasts, or nothing when the
// control plane did not say.
func expiresIn(expiry time.Time) string {
	if expiry.IsZero() {
		return ""
	}
	mins := int(time.Until(expiry).Round(time.Minute) / time.Minute)
	if mins < 1 {
		return "\nThe code expires within a minute."
	}
	if mins == 1 {
		return "\nThe code works for 1 more minute."
	}
	return fmt.Sprintf("\nThe code works for %d minutes.", mins)
}

var errCodeExpired = apiError{Code: "expired_token",
	Message: "nobody approved this machine before the code expired, so it has been thrown away",
	Hint:    "run gg login again for a fresh link and code, and pass them on straight away"}

// loginError puts the flow's failures in gg's own shape — `[code] message`, and
// a hint that says what to run — because the RFC's error codes are what an
// agent branches on, and oauth2's own wording is not written for either reader.
func loginError(api string, err error) error {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		switch re.ErrorCode {
		case "access_denied":
			return apiError{Code: "access_denied",
				Message: "your human declined to give this machine access, so the code has been thrown away",
				Hint:    "ask them before running gg login again for a fresh code; do not retry on your own"}
		case "expired_token":
			return errCodeExpired
		case "":
			status := 0
			if re.Response != nil {
				status = re.Response.StatusCode
			}
			return fmt.Errorf("the control plane at %s refused to sign this machine in (HTTP %d)", api, status)
		default:
			msg := re.ErrorDescription
			if msg == "" {
				msg = "the control plane refused to sign this machine in"
			}
			return apiError{Code: re.ErrorCode, Message: msg,
				Hint: "run gg login again for a fresh code; if it repeats, tell your human what it says"}
		}
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return fmt.Errorf("cannot reach control plane at %s: %w", api, err)
	}
	return fmt.Errorf("signing in through %s failed: %w", api, err)
}

func cmdWhoami() error {
	var out struct {
		Account    string   `json:"account"`
		Client     string   `json:"client"`
		Can        []string `json:"can"`
		BaseDomain string   `json:"base_domain"`
		Registry   string   `json:"registry"`
		Platform   string   `json:"platform"`
	}
	if err := call("GET", "/v1/whoami", nil, &out); err != nil {
		return err
	}
	fmt.Printf("account   %s\n", out.Account)
	fmt.Printf("client    %s\n", out.Client)
	fmt.Printf("can       %s\n", strings.Join(out.Can, ", "))
	fmt.Printf("registry  %s\n", out.Registry)
	fmt.Printf("platform  %s\n", out.Platform)
	return nil
}

// clientName is what the approval page will call this machine, and what the
// credential is named after. Named honestly: the human is about to make a
// security decision from this string, so it should say what is really asking,
// and where.
//
// Every part of it is a hint the client supplies about itself, and none of it is
// verified by anything — which is exactly why it always ends in a hostname the
// reader can recognise. "Claude Code on viktor-mbp" is useful because of the
// second half; the first half is only a convenience.
//
// GITHUB_ACTIONS used to be in this list and returned the bare string "GitHub
// Actions", with no host at all. It is gone, for two reasons that point the same
// way. It was the one entry that produced a *credible institutional* name rather
// than a description of a machine, so anything that could get a human to approve
// a request while GITHUB_ACTIONS was set in the environment could have that
// approval read as a CI system rather than as whatever was really asking. And it
// was never needed: CI does not sign in. A pipeline gets its credential from
// `gg creds create`, run by a human on a machine that is already
// authorised, which is a name the caller states outright rather than one gg
// guesses from the environment.
func clientName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "an unknown machine"
	}
	// Agent harnesses advertise themselves in the environment. Using that means
	// the approval page says "Claude Code on viktor-mbp" rather than "gg".
	for _, key := range []string{"CLAUDECODE", "CLAUDE_CODE", "CURSOR_AGENT", "AIDER"} {
		if os.Getenv(key) == "" {
			continue
		}
		switch key {
		case "CLAUDECODE", "CLAUDE_CODE":
			return "Claude Code on " + host
		case "CURSOR_AGENT":
			return "Cursor on " + host
		case "AIDER":
			return "Aider on " + host
		}
	}
	return "gg on " + host
}
