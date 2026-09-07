package main

// What `gg transfer` says, and what it sends.
//
// The output is the thing being asserted, not decoration. A transfer is the one
// command that finishes with nothing having happened — the project moves when
// somebody else presses a button, days later — and a user who reads "offered" as
// "done" stops paying attention at exactly the wrong moment.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestTransferSaysNothingHasChangedYet(t *testing.T) {
	var got map[string]any
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/projects/shop/transfer" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(`{"next":"we emailed them@example.com."}`))
	})

	out := capture(t, func() {
		if err := cmdTransfer("shop", "them@example.com", ""); err != nil {
			t.Fatal(err)
		}
	})

	if got["email"] != "them@example.com" {
		t.Errorf("the offer did not carry the address: %v", got)
	}
	// The two facts a reader must not miss: it is an offer, and it is waiting.
	if !strings.Contains(out, "offered shop to them@example.com") {
		t.Errorf("output does not say what was offered:\n%s", out)
	}
	if !strings.Contains(out, "nothing has changed yet") {
		t.Errorf("output lets a transfer read as finished:\n%s", out)
	}
}

// --as is only ever needed because names are unique per account. It has to reach
// the API, or the handover is refused for a reason the user has already fixed.
func TestTransferPassesTheLandingName(t *testing.T) {
	var got map[string]any
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(`{"next":""}`))
	})

	_ = capture(t, func() {
		if err := cmdTransfer("shop", "them@example.com", "shop-prod"); err != nil {
			t.Fatal(err)
		}
	})
	if got["name"] != "shop-prod" {
		t.Errorf("--as did not reach the API: %v", got)
	}
}

// Withdrawing is a DELETE on the same path, and it says the link is dead —
// because the person who made the offer is now wondering about the email they
// sent.
func TestTransferWithdrawSaysTheLinkIsDead(t *testing.T) {
	var method, path string
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})

	out := capture(t, func() {
		if err := cmdTransferWithdraw("shop"); err != nil {
			t.Fatal(err)
		}
	})
	if method != http.MethodDelete || path != "/v1/projects/shop/transfer" {
		t.Errorf("withdraw hit %s %s", method, path)
	}
	if !strings.Contains(out, "no longer works") {
		t.Errorf("withdraw does not say the emailed link is dead:\n%s", out)
	}
}

// A pending offer belongs with the roster: "who can reach this" and "who is about
// to start paying for it" are the same question a day apart, and nobody would
// think to run a second command to ask the second one.
func TestMembersShowsAPendingOffer(t *testing.T) {
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"owner": "founder@example.com",
			"members": [{"email": "them@example.com", "role": "editor"}],
			"offer": {"to": "them@example.com", "name": "shop-prod", "offered_by": "founder@example.com"}
		}`))
	})

	out := capture(t, func() {
		if err := cmdMembers("shop"); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{
		"owner (pays for it)",
		"offered ownership, not yet accepted",
		`called "shop-prod" in their account`,
		"--withdraw",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("roster is missing %q:\n%s", want, out)
		}
	}
}

// The ordinary case prints no offer line at all. An empty "offer:" heading over
// nothing would read as one that had been withdrawn.
func TestMembersSaysNothingAboutAnOfferWhenThereIsNone(t *testing.T) {
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"owner": "founder@example.com", "members": []}`))
	})

	out := capture(t, func() {
		if err := cmdMembers("shop"); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "offered") {
		t.Errorf("a roster with no offer mentions one:\n%s", out)
	}
}
