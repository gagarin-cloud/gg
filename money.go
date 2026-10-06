package main

import (
	"strconv"
	"strings"
)

// The two ways an amount of micro-dollars is written down. Everything gg prints
// goes through one of them, and both are ports of the console's money() and
// proseMicro() (gagarin-cloud/my) — and the same as the engine's billing.Money
// and billing.Prose — with the same test vectors, so a reader never sees two
// forms of one number.
//
// Four decimals, never three: where a point groups thousands "$5.000" reads as
// five thousand dollars, and a fourth decimal cannot be mistaken for a group.
// Integer arithmetic throughout, truncating toward zero — a float rounds, and a
// figure must never claim money that was not there.

// moneyDecimals is how many decimals a reading shows. 10^(6-moneyDecimals)
// micro-dollars is the last digit.
const moneyDecimals = 4

const microPerDollar = 1_000_000

// moneyParts splits micro-dollars into a sign, whole dollars and the first
// four decimals, truncated toward zero. The absolute value is a uint64 so
// math.MinInt64 does not overflow on the way.
func moneyParts(microUSD int64) (sign string, whole uint64, fraction uint64) {
	abs := uint64(microUSD)
	if microUSD < 0 {
		sign, abs = "-", uint64(-(microUSD+1))+1
	}
	return sign, abs / uint64(microPerDollar), abs % uint64(microPerDollar) / 100
}

// money is an amount as a reading: a column, the meter under `gg status`.
// Always four decimals and no thousands separators, so a column lines up and a script can read it back: 5000000 is "$5.0000",
// -10509 is "-$0.0105". A non-zero amount too small to reach the last digit is
// "< $0.0001" — a meter that reads zero while something runs is the one thing
// it must not do.
func money(microUSD int64) string {
	sign, whole, fraction := moneyParts(microUSD)
	if microUSD != 0 && whole == 0 && fraction == 0 {
		return sign + "< $0.0001"
	}
	return sign + "$" + strconv.FormatUint(whole, 10) + "." + pad4(fraction)
}

// prose is an amount inside a sentence, like the terms `gg referral` states.
// Whole dollars carry no decimals and group thousands ("$2,000"), whole cents
// carry two ("$1.50"), anything finer carries four ("$0.0140"); zero is "$0".
// So "matched up to $50" stays $50.
func prose(microUSD int64) string {
	if microUSD == 0 {
		return "$0"
	}
	sign, whole, fraction := moneyParts(microUSD)
	if whole == 0 && fraction == 0 {
		return sign + "< $0.0001"
	}
	dollars := sign + "$" + groupThousands(strconv.FormatUint(whole, 10))
	switch {
	case fraction == 0:
		return dollars
	case fraction%100 == 0:
		return dollars + "." + pad4(fraction)[:2]
	default:
		return dollars + "." + pad4(fraction)
	}
}

func pad4(n uint64) string {
	s := strconv.FormatUint(n, 10)
	return strings.Repeat("0", moneyDecimals-len(s)) + s
}

// groupThousands is "1200" → "1,200", the en-US grouping toLocaleString gives
// the console.
func groupThousands(digits string) string {
	var b strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(d)
	}
	return b.String()
}
