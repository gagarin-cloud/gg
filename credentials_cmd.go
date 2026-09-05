package main

// What has access to this account, and how a second thing gets it.
//
// The endpoints existed before these commands did, which is the whole reason
// this file exists. Somebody setting up CI had to read gg's source to work out
// how to get a credential for it, and what they found was two unrelated features
// that could be bent into the shape: XDG_CONFIG_HOME to redirect where the
// credential file lands, and GITHUB_ACTIONS to make clientName() produce a
// recognisable label. Both work. Neither was designed for it, one of them was a
// way to put an unverified institutional name in front of a human's approval
// decision, and the whole dance still needed a click in an inbox for something
// that has no inbox.
//
// So: `gg creds create` mints one from the credential you already hold. No
// email, no approval, and nothing written to disk — the secret is printed once,
// because a CI credential's destination is a secret store somewhere else.

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

type credentialRow struct {
	ID         int64      `json:"id"`
	Client     string     `json:"client"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	Current    bool       `json:"current"`
	Minted     bool       `json:"minted"`
}

func cmdCredentialsList() error {
	var out struct {
		Credentials []credentialRow `json:"credentials"`
	}
	if err := call("GET", "/v1/credentials", nil, &out); err != nil {
		return err
	}
	if len(out.Credentials) == 0 {
		fmt.Println("nothing has access to this account, which should be impossible from here.")
		return nil
	}

	rows := [][]string{{"", "ID", "NAME", "KIND", "CAN", "LAST USED", "EXPIRES"}}
	for _, c := range out.Credentials {
		// The one you are holding is marked, because the first thing somebody
		// does with this list is look for the one they must not revoke.
		mark := " "
		if c.Current {
			mark = "*"
		}
		kind := "approved"
		if c.Minted {
			kind = "minted"
		}
		sort.Strings(c.Scopes)
		can := strings.Join(c.Scopes, ",")
		if can == "" {
			can = "—"
		}
		rows = append(rows, []string{
			mark, fmt.Sprintf("%d", c.ID), c.Client, kind, can,
			lastUsed(c.LastUsedAt), until(c.ExpiresAt),
		})
	}
	printAligned(rows)

	fmt.Printf("\n  * the credential this command used\n")
	fmt.Printf("  approved = a human clicked a link for it; minted = issued by another credential\n")
	fmt.Printf("\n  gg creds revoke <id>\n")
	return nil
}

// cmdCredentialsCreate mints one and prints it, once.
//
// It deliberately does not offer to write the credential anywhere. The file this
// CLI keeps is for the machine a human authorised; a minted credential's
// destination is a CI system's secret store, and writing it to disk on the way
// there would leave a copy in the shell history of the machine that made it.
func cmdCredentialsCreate(name string, days int) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("say what this credential is for\n" +
			"  hint: gg creds create --name \"github actions: acme/web\"\n" +
			"  it is what you will read in `gg creds` when deciding whether it is still wanted")
	}
	body := map[string]any{"name": name}
	if days > 0 {
		body["expires_in_days"] = days
	}
	var out struct {
		ID         int64     `json:"id"`
		Credential string    `json:"credential"`
		Account    string    `json:"account"`
		Name       string    `json:"name"`
		Scopes     []string  `json:"scopes"`
		ExpiresAt  time.Time `json:"expires_at"`
		API        string    `json:"api"`
	}
	if err := call("POST", "/v1/credentials", body, &out); err != nil {
		return err
	}

	// The secret goes to stdout on its own line and nothing else does, so that
	// piping this into a secret store is a sane thing to do. Everything else
	// goes to stderr for the same reason.
	fmt.Fprintf(os.Stderr, "minted %q for %s\n", out.Name, out.Account)
	fmt.Fprintf(os.Stderr, "  can       %s\n", strings.Join(out.Scopes, ", "))
	fmt.Fprintf(os.Stderr, "  expires   %s\n", out.ExpiresAt.Format("2 January 2006"))
	fmt.Fprintf(os.Stderr, "  id        %d   (gg creds revoke %d)\n\n", out.ID, out.ID)

	fmt.Println(out.Credential)

	fmt.Fprintf(os.Stderr, `
That is the only time it is shown, and gagarin stores no copy — it keeps a hash.
If you lose it, revoke it and mint another.

Give it to CI as GAGARIN_TOKEN. For GitHub Actions:

  gh secret set GAGARIN_TOKEN --body '<the line above>'

and in the workflow:

  env:
    GAGARIN_TOKEN: ${{ secrets.GAGARIN_TOKEN }}

It can deploy and it cannot destroy, so a pipeline holding it cannot delete a
project, a service or a database — those need a human either way.
`)
	return nil
}

func cmdCredentialsRevoke(id string) error {
	if err := call("DELETE", "/v1/credentials/"+id, nil, nil); err != nil {
		return err
	}
	fmt.Printf("revoked %s. Anything using it is now unauthenticated.\n", id)
	return nil
}

// lastUsed is ago (rollback.go) over a pointer, because "never used" and "used
// at the zero time" are different facts and only one of them is worth printing.
func lastUsed(t *time.Time) string {
	if t == nil {
		return "never"
	}
	return ago(*t)
}

// until is the other direction, rendered the way somebody reads it: as a
// distance, not a timestamp nobody subtracts in their head.
func until(t *time.Time) string {
	if t == nil {
		return "never"
	}
	d := time.Until(*t)
	if d <= 0 {
		return "expired"
	}
	if d < 48*time.Hour {
		return fmt.Sprintf("in %dh", int(d.Hours()))
	}
	return fmt.Sprintf("in %dd", int(d.Hours()/24))
}

// printAligned is the same column arithmetic gg status does: widths from the
// content, so nothing is truncated and nothing is padded to a guess.
func printAligned(rows [][]string) {
	if len(rows) == 0 {
		return
	}
	w := make([]int, len(rows[0]))
	for _, r := range rows {
		for i, c := range r {
			if n := len([]rune(c)); i < len(w) && n > w[i] {
				w[i] = n
			}
		}
	}
	for _, r := range rows {
		var b strings.Builder
		b.WriteString("  ")
		for i, c := range r {
			if i == len(r)-1 {
				b.WriteString(c)
				break
			}
			b.WriteString(c)
			b.WriteString(strings.Repeat(" ", w[i]-len([]rune(c))+2))
		}
		fmt.Println(strings.TrimRight(b.String(), " "))
	}
}
