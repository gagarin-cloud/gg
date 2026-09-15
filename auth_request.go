package main

// What `gg login EMAIL` says, and why it says the same thing to everybody.
//
// Kept apart from the rest of auth.go because the wording here is the entire
// user-facing surface of gagarin's onboarding: it is read by an agent, repeated
// to a human, and acted on without either of them seeing the API. Getting it
// wrong does not break a test, it sends somebody to stare at an empty inbox.
//
// # The one answer
//
// Before 2026-09-16 the control plane emailed a link to whatever address it was
// given. That was used as a mailer, so it no longer does: an address with an
// account gets its link, and an address without one gets nothing until the
// person writes to signup@ themselves. See brain/docs/049 in the gagarin repo.
//
// Which of the two applies is deliberately NOT in the answer, and this file is
// where that decision costs something. Telling them apart would let anybody test
// whether an address has a gagarin account, one request at a time, from an
// endpoint that needs no credential. So the control plane says `pending` to
// both, and the text below says both halves and lets the person recognise which
// one is theirs. It is two sentences longer than it could be, and that is the
// price of not answering a question nobody should be able to ask.

import (
	"fmt"
	"net/url"
	"strings"
)

// signupRequest is what the control plane answers to `gg login EMAIL`.
type signupRequest struct {
	Claim string `json:"claim"`
	// What happened, in the only two states that do not leak whether the address
	// has an account: "pending" — either the link is in their inbox or they have
	// to write to us, and we will not say which — or "logged", which is a
	// property of the deployment (no mail provider) rather than of the address.
	//
	// gg used to print "the email we just sent" whatever the answer was, which on
	// 2026-09-01 was said twice about an email that was never sent. The lesson
	// stands even though the states changed: say only what is certainly true.
	Delivery string `json:"delivery"`
	// Where somebody without an account has to write. Empty means this deployment
	// never told us, and the instruction below degrades to naming no address
	// rather than inventing one.
	SignupAddress string `json:"signup_address"`
}

func cmdLoginRequest(email string) error {
	if strings.TrimSpace(email) == "" {
		return fmt.Errorf("usage: gg login EMAIL\n" +
			"  ask your human for their address — do not guess it")
	}
	var out signupRequest
	body := map[string]string{"email": email, "client": clientName()}
	if err := callAnon("POST", "/v1/signup", body, &out); err != nil {
		return err
	}

	fmt.Printf(`asked %s to approve this machine.

%s

Then run:
  gg login --claim %s
`, email, loginInstruction(out), out.Claim)
	return nil
}

/*
loginInstruction is the paragraph an agent reads out to its human.

Written as one instruction covering both cases rather than a branch, because the
control plane will not tell us which case it is. The order matters: the inbox is
named first because most people asking to sign in already have an account, and
the person who does not will read one sentence further and find their answer.
*/
func loginInstruction(out signupRequest) string {
	if out.Delivery == "logged" {
		return fmt.Sprintf(`No email was sent: this gagarin has no mail provider configured, so the
approval link went to the control plane's log instead. Somebody with access to
those logs has to open it. It carries code %s, which should match this one.`, out.Claim)
	}

	where := out.SignupAddress
	if where == "" {
		// Nothing useful to point at. Say so plainly rather than printing an
		// address that does not exist, which would waste somebody's afternoon.
		where = "the signup address for this gagarin, which it did not tell us"
	}

	return fmt.Sprintf(`Tell your human this, and give them the code %[1]s:

  If they already have a gagarin account, a link is in their inbox now. Pressing
  its button grants this machine access. The email shows code %[1]s, which should
  match this one.

  If they do not have an account yet, nothing has been emailed — gagarin does not
  write to an address until that address writes to it. They send a mail to
  %[2]s with %[1]s in the subject, and the reply carries
  the same button. The account is created when they press it, with $5 on it and
  no card asked for.%[3]s

They will know which of the two is them. Do not guess on their behalf, and do not
tell them the account does or does not exist — we are not told either.`,
		out.Claim, where, mailtoHint(where, out.Claim))
}

// mailtoHint offers the pre-filled link, which is the difference between an
// instruction somebody follows and one they mean to get around to. Most
// terminals make it clickable; the ones that do not still show a readable
// address, so nothing is lost.
func mailtoHint(where, code string) string {
	if !strings.Contains(where, "@") {
		return ""
	}
	return fmt.Sprintf("\n  Ready to send: mailto:%s?subject=%s",
		where, url.QueryEscape(code))
}
