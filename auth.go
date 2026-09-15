package main

// Onboarding, from the CLI's side.
//
// `gg login` exists so a human never has to copy a secret and an agent never has
// to hold one. The agent runs it twice — once to ask, once to collect — and the
// human's only action happens in their inbox.
//
// One word, because there is one request. Asking costs the same call whether the
// address has an account or not, and the control plane still answers identically
// either way — deliberately, so that this endpoint cannot be used to test whether
// an address is registered. `gg signup` and `gg auth` were two names for the two
// halves of that, and the pair kept implying a first-time path that does not
// exist — an implication that reached the control plane's own 401 hint, where it
// told a machine to "run gg auth" to re-authorise, which was advice that could
// not work.
//
// What changed on 2026-09-16 is what happens behind that identical answer. An
// address with an account is emailed a link, as always. An address without one is
// emailed nothing at all: the human writes to signup@ from it and is answered
// there. gagarin does not write to an address until that address writes to it,
// because the old behaviour was being used to mail strangers. The wording that
// covers both without saying which is in auth_request.go.
//
// The two invocations do stay two, for a reason that has nothing to do with
// first-versus-fifth: between them the agent has to tell its human what to press
// and which code to match, and it can only say that between commands. Fusing
// them would block for the whole approval window before the agent could speak.

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// cmdLogin routes the two halves, and the routing is here rather than in the
// command wiring because it is a decision rather than a flag.
//
// An address and a code cannot be mistaken for each other — one contains an @,
// and the other is eight characters drawn from an alphabet that deliberately has
// no @ in it — so a bare argument is read for what it is. An agent that types
// `gg login ABCD-1234` meant the second half, and refusing that on syntax would
// be pedantry.
func cmdLogin(arg, claim string) error {
	if claim == "" && arg != "" && !strings.Contains(arg, "@") {
		arg, claim = "", arg
	}
	switch {
	case arg != "" && claim != "":
		return fmt.Errorf("gg login takes an address or a code, not both\n" +
			"  to ask:     gg login <your human's email>\n" +
			"  to collect: gg login --claim <the code it printed>")
	case claim != "":
		return cmdLoginCollect(claim)
	case arg != "":
		return cmdLoginRequest(arg)
	}
	// Neither: say what to do rather than what is missing.
	if creds, err := loadCredentials(); err == nil && creds.Credential != "" {
		fmt.Printf("this machine already acts as %s (%s)\n", creds.Account, creds.Client)
		fmt.Printf("to authorise it again: gg login %s\n", creds.Account)
		return nil
	}
	return fmt.Errorf("usage: gg login EMAIL, then gg login --claim CODE\n" +
		"  ask your human for their address — do not guess it")
}

func cmdLoginCollect(claim string) error {
	fmt.Printf("waiting for a human to approve %s ...\n", claim)
	// Polling, not a webhook: the CLI runs on a laptop behind NAT, and an agent
	// harness will not host a callback. The control plane tells us how long to
	// wait between attempts so the cadence is its decision, not ours.
	deadline := time.Now().Add(12 * time.Minute)
	for {
		var out struct {
			Status     string   `json:"status"`
			Credential string   `json:"credential"`
			Account    string   `json:"account"`
			Client     string   `json:"client"`
			Scopes     []string `json:"scopes"`
			ExpiresAt  string   `json:"expires_at"`
			API        string   `json:"api"`
			RetryAfter int      `json:"retry_after"`
		}
		err := callAnon("POST", "/v1/claim", map[string]string{"claim": claim}, &out)
		if err != nil {
			return err
		}
		if out.Status == "approved" {
			api := out.API
			if api == "" {
				api = apiBase()
			}
			path, err := saveCredentials(&credentials{
				API:        api,
				Credential: out.Credential,
				Account:    out.Account,
				Client:     out.Client,
				Scopes:     out.Scopes,
				ExpiresAt:  out.ExpiresAt,
			})
			if err != nil {
				return fmt.Errorf("approved, but the credential could not be saved: %w", err)
			}
			fmt.Printf("\nthis machine now acts as %s\n", out.Account)
			fmt.Printf("  credential stored in %s\n", path)
			fmt.Printf("  it can %s — deleting anything needs a fresh approval\n",
				strings.Join(out.Scopes, ", "))

			// The gagarin credential is also the registry credential, so there is
			// nothing to ask for and no reason to make somebody run a second
			// command before their first push. Best effort: docker may not be
			// installed on this machine, and that is not a failed authorisation —
			// reading logs and status needs no docker at all.
			if err := cmdRegistryLogin(); err != nil {
				fmt.Printf("\n  (docker is not logged in to the registry yet: %v)\n", err)
				fmt.Printf("  run `gg registry login` before your first push\n")
			}

			fmt.Printf("\nnothing to export. try: gg projects\n")
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("nobody approved %s in time\n  ask your human to check their inbox, then: gg login <email>", claim)
		}
		wait := time.Duration(out.RetryAfter) * time.Second
		if wait <= 0 {
			wait = 2 * time.Second
		}
		time.Sleep(wait)
	}
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

// clientName is what the approval email will call this machine. Named honestly:
// the human is about to make a security decision from this string, so it should
// say what is really asking, and where.
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
// was never needed: CI does not sign up. A pipeline gets its credential from
// `gg creds create`, run by a human on a machine that is already
// authorised, which is a name the caller states outright rather than one gg
// guesses from the environment.
func clientName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "an unknown machine"
	}
	// Agent harnesses advertise themselves in the environment. Using that means
	// the email says "Claude Code on viktor-mbp" rather than "gg".
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
