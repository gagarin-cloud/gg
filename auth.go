package main

// Onboarding, from the CLI's side.
//
// `gg signup` and `gg auth` exist so a human never has to copy a secret and an
// agent never has to hold one. The agent runs both; the human's only action
// happens in their inbox.

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func cmdSignup(email string) error {
	if strings.TrimSpace(email) == "" {
		return fmt.Errorf("usage: gg signup EMAIL\n" +
			"  ask your human for their address — do not guess it")
	}
	var out struct {
		Claim     string `json:"claim"`
		ExpiresIn int    `json:"expires_in"`
		// What happened to the email: "sent", "already_sent", or "logged".
		// gg used to print "the email we just sent" whatever the answer was,
		// which on 2026-09-01 was said twice about an email that was never
		// sent — the control plane knew and the JSON did not carry it.
		Delivery string `json:"delivery"`
	}
	body := map[string]string{"email": email, "client": clientName()}
	if err := callAnon("POST", "/v1/signup", body, &out); err != nil {
		return err
	}

	// One paragraph per outcome, because the instruction differs and not only
	// the wording: "check your inbox" is useless advice when nothing was sent
	// there, and "we just sent one" is wrong when the one that matters is ten
	// minutes old.
	said := fmt.Sprintf(`Tell your human to click the button in the email we just sent. It shows code
%s, which should match this one. That single click creates the account and
grants this machine access.`, out.Claim)

	switch out.Delivery {
	case "already_sent":
		said = fmt.Sprintf(`An approval email for this code is already in their inbox and we did not send
another. It shows code %s, which should match this one. That single click
creates the account and grants this machine access.`, out.Claim)
	case "logged":
		said = fmt.Sprintf(`No email was sent: this gagarin has no mail provider configured, so the
approval link went to the control plane's log instead. Somebody with access to
those logs has to open it. It carries code %s, which should match this one.`, out.Claim)
	}

	fmt.Printf(`asked %s to approve this machine.

%s

Then run:
  gg auth --claim %s
`, email, said, out.Claim)
	return nil
}

func cmdAuth(claim string) error {
	if claim == "" {
		// No code: say what to do rather than what is missing.
		if creds, err := loadCredentials(); err == nil && creds.Credential != "" {
			fmt.Printf("this machine already acts as %s (%s)\n", creds.Account, creds.Client)
			fmt.Printf("to authorise it again: gg signup %s\n", creds.Account)
			return nil
		}
		return fmt.Errorf("usage: gg auth --claim CODE\n" +
			"  get a code first: gg signup <your human's email>")
	}

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
			return fmt.Errorf("nobody approved %s in time\n  ask your human to check their inbox, then: gg signup <email>", claim)
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
func clientName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "an unknown machine"
	}
	// Agent harnesses advertise themselves in the environment. Using that means
	// the email says "Claude Code on viktor-mbp" rather than "gg".
	for _, key := range []string{"CLAUDECODE", "CLAUDE_CODE", "CURSOR_AGENT", "AIDER", "GITHUB_ACTIONS"} {
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
		case "GITHUB_ACTIONS":
			return "GitHub Actions"
		}
	}
	return "gg on " + host
}
