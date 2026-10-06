package main

// `gg referral`: the invite link, and what it has earned.
//
// Only an account that has paid for something can invite, so a new account is
// told when invites unlock instead of being handed nothing. The engine decides
// who is eligible and what the terms are; gg prints what it is told, and the
// invited users it lists are identified by the engine's display name only.

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type referralTotals struct {
	Invited                 int   `json:"invited"`
	EarningNow              int   `json:"earning_now"`
	InvitedToppedUpMicroUSD int64 `json:"invited_topped_up_micro_usd"`
	EarnedMicroUSD          int64 `json:"earned_micro_usd"`
}

type referralInvitee struct {
	Display          string `json:"display"`
	JoinedAt         string `json:"joined_at"`
	WindowEndsAt     string `json:"window_ends_at"`
	Earning          bool   `json:"earning"`
	Status           string `json:"status"`
	ToppedUpMicroUSD int64  `json:"topped_up_micro_usd"`
	EarnedMicroUSD   int64  `json:"earned_micro_usd"`
}

type referralView struct {
	Eligible         bool              `json:"eligible"`
	Code             string            `json:"code"`
	Link             string            `json:"link"`
	RatePercent      int               `json:"rate_percent"`
	WindowMonths     int               `json:"window_months"`
	MatchCapMicroUSD int64             `json:"match_cap_micro_usd"`
	Totals           referralTotals    `json:"totals"`
	Invited          []referralInvitee `json:"invited"`
}

func newReferralCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "referral",
		Short: "your invite link, and what it has earned",
		Long: `Show your invite link and what the people you invited have brought in.

  gg referral

Once you have topped up for the first time you get a link and a code. A friend
who signs up through it gets their first top-up matched, up to a cap; you earn a
share of what they top up for a few months, as credit on your balance — never
cash. They can also pass the code to gg: gg login --ref CODE.

Below the totals is one row per person you invited, by their GitHub login or a
masked email: when they joined, what they have topped up, what you earned from
it, and when the earning window ends. A void referral earns nothing more.`,
		Args: usageArgs(0, 0, "usage: gg referral"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdReferral()
		},
	}
}

func cmdReferral() error {
	var out referralView
	if err := call("GET", "/v1/referrals", nil, &out); err != nil {
		return err
	}
	printReferral(out)
	return nil
}

// refDate is a day, in the reader's own clock; a value that does not parse is
// printed as it came rather than hidden.
func refDate(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.Local().Format("2006-01-02")
}

func printReferral(v referralView) {
	if !v.Eligible {
		fmt.Println("invite links unlock after your first top-up")
		fmt.Println("\n  top up in the console at https://my.gagarin.cloud, then run gg referral again")
		return
	}
	fmt.Printf("your invite link  %s\n", v.Link)
	fmt.Printf("your code         %s   (a friend can use it with: gg login --ref %s)\n", v.Code, v.Code)
	fmt.Printf("\nthe deal: a friend's first top-up is matched up to %s; you earn %d%% of what they top up for %d months, as credit\n",
		prose(v.MatchCapMicroUSD), v.RatePercent, v.WindowMonths)

	if len(v.Invited) == 0 {
		fmt.Println("\nnobody has signed up through your link yet")
		return
	}
	t := v.Totals
	fmt.Printf("\n%d invited, %d earning now, %s topped up by them, %s earned by you\n",
		t.Invited, t.EarningNow, prose(t.InvitedToppedUpMicroUSD), prose(t.EarnedMicroUSD))

	fmt.Printf("\n%-28s  %-10s  %-10s  %-10s  %-12s  %s\n",
		"USER", "JOINED", "TOPPED UP", "YOU EARNED", "WINDOW", "STATUS")
	for _, in := range v.Invited {
		window := refDate(in.WindowEndsAt)
		switch {
		case in.Status == "void":
			window = "-"
		case !in.Earning:
			window = "ended " + window
		default:
			window = "ends " + window
		}
		status := in.Status
		if status == "active" && !in.Earning {
			status = "ended"
		}
		fmt.Printf("%-28s  %-10s  %-10s  %-10s  %-12s  %s\n",
			strings.TrimSpace(in.Display), refDate(in.JoinedAt),
			money(in.ToppedUpMicroUSD), money(in.EarnedMicroUSD), window, status)
	}
}
