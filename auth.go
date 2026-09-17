package main

// Onboarding, from the CLI's side.
//
// `gg login` exists so a human never has to copy a secret and an agent never has
// to hold one. Since 2026-09-17 it is the OAuth 2.0 device authorization grant
// (RFC 8628): gg asks the control plane for a code, prints a link, and waits
// while a human opens it, signs in with GitHub or Google, and approves this
// machine by name. Then the credential lands in the file, as it always did.
//
// It used to be two runs — `gg login EMAIL` to ask and `gg login --claim CODE`
// to collect — with the human's part happening in an inbox. That went with email
// sign-in; see brain/docs/051 in the gagarin repo. The reason it was two runs
// still exists, though: an agent has to tell its human what to open before the
// human can open it, and a command that blocks is a command whose output an
// agent may not see until it exits. So gg prints everything the human needs
// first, alone and in full, before it starts waiting — and the skill tells an
// agent to run it where it can read that output while it waits.
//
// Nothing here is signed up for. Signing in, signing up and authorising another
// machine are the same page; which one it was is the control plane's business.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

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

// cmdLogin always starts the flow, even on a machine that already holds a
// credential. The obvious alternative — say "already acts as" and stop — is a
// trap: the usual reason to run `gg login` on such a machine is that the stored
// credential stopped working, and a command that refuses to replace it sends
// the reader off to delete a file by hand.
func cmdLogin() error {
	api := apiBase()
	conf := deviceConfig(api)

	// One client with a timeout for every request the flow makes, so a control
	// plane that accepts a connection and never answers cannot hang gg for the
	// whole fifteen minutes.
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient,
		&http.Client{Timeout: 30 * time.Second})

	if creds, err := loadCredentials(); err == nil && creds.Credential != "" && creds.Account != "" {
		fmt.Printf("this machine currently acts as %s; approving replaces that credential\n\n", creds.Account)
	}

	da, err := conf.DeviceAuth(ctx, oauth2.SetAuthURLParam("label", clientName()))
	if err != nil {
		return loginError(api, err)
	}

	link := da.VerificationURIComplete
	if link == "" {
		link = da.VerificationURI
	}
	fmt.Printf(`this machine is asking for access to gagarin as "%s".

Tell your human to open this link, sign in with GitHub or Google, and approve:

  %s

If the link does not open, go to %s and enter the code %s.
The page shows that code and the name above; both should match.%s

waiting for approval ...
`, clientName(), link, da.VerificationURI, da.UserCode, expiresIn(da.Expiry))

	tok, err := conf.DeviceAccessToken(ctx, da)
	if err != nil {
		// oauth2 stops polling at the expiry the control plane gave, and says
		// so as a context deadline. That is the same fact as expired_token.
		if errors.Is(err, context.DeadlineExceeded) && !da.Expiry.IsZero() && !time.Now().Before(da.Expiry) {
			return errCodeExpired
		}
		return loginError(api, err)
	}

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
		return ""
	}
	return fmt.Sprintf("\nThe code works for %d minutes.", mins)
}

var errCodeExpired = apiError{Code: "expired_token",
	Message: "nobody approved this machine before the code expired",
	Hint:    "run gg login again for a fresh link, and pass it on straight away"}

// loginError puts the flow's failures in gg's own shape — `[code] message`, and
// a hint that says what to run — because the RFC's error codes are what an
// agent branches on, and oauth2's own wording is not written for either reader.
func loginError(api string, err error) error {
	var re *oauth2.RetrieveError
	switch {
	case errors.As(err, &re):
		switch re.ErrorCode {
		case "access_denied":
			return apiError{Code: "access_denied",
				Message: "your human declined to give this machine access",
				Hint:    "ask them before running gg login again; do not retry on your own"}
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
				Hint: "run gg login again; if it repeats, tell your human what it says"}
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
