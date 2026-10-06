package main

import (
	"math"
	"testing"
)

// The vectors are the ones the console's money() and proseMicro() are tested
// with (gagarin-cloud/my), and the engine's billing.Money and billing.Prose
// with, kept identical so no two screens can drift apart.
func TestMoneyIsFourDecimalsTruncated(t *testing.T) {
	for _, tc := range []struct {
		micro int64
		want  string
	}{
		{5_000_000, "$5.0000"},
		{986_697_319, "$986.6973"},
		{-10_509, "-$0.0105"}, // truncated: rounding would claim -$0.0106
		{14_000, "$0.0140"},
		{0, "$0.0000"},
		{99, "< $0.0001"},
		{100, "$0.0001"},
		{-99, "-< $0.0001"},
		{-100, "-$0.0001"},
		{-1, "-< $0.0001"},
		{1_234_567, "$1.2345"}, // truncation, not rounding
		{1_234_567_890_123, "$1234567.8901"},
		{2_000_000_000, "$2000.0000"}, // no thousands separators in a reading
		{math.MaxInt64, "$9223372036854.7758"},
		{math.MinInt64, "-$9223372036854.7758"},
	} {
		if got := money(tc.micro); got != tc.want {
			t.Errorf("money(%d) = %q, want %q", tc.micro, got, tc.want)
		}
	}
}

func TestProseDropsTheDecimalsASentenceDoesNotNeed(t *testing.T) {
	for _, tc := range []struct {
		micro int64
		want  string
	}{
		{20_000_000, "$20"},
		{1_200_000_000, "$1,200"},
		{2_000_000_000, "$2,000"},
		{1_500_000, "$1.50"},
		{290_000, "$0.29"},
		{14_000, "$0.0140"},
		{3_026_000, "$3.0260"},
		{1_234_567, "$1.2345"},
		{-10_509, "-$0.0105"},
		{-20_000_000, "-$20"},
		{0, "$0"},
		{99, "< $0.0001"},
		{100, "$0.0001"},
		{-1, "-< $0.0001"},
		{1_234_567_890_123, "$1,234,567.8901"},
	} {
		if got := prose(tc.micro); got != tc.want {
			t.Errorf("prose(%d) = %q, want %q", tc.micro, got, tc.want)
		}
	}
}
