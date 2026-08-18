package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseEnvFile(t *testing.T) {
	p := writeTemp(t, ".env", `
# a comment
DATABASE_URL=postgres://user:pass@host:5432/db

export EXPORTED=yes
QUOTED="hello world"
SINGLE='raw $NOT_EXPANDED'
EMPTY=
SPACED  =  trimmed
EQUALS_IN_VALUE=a=b=c
ESCAPED="line\nbreak"
`)
	got, err := parseEnvFile(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]string{
		"DATABASE_URL":    "postgres://user:pass@host:5432/db",
		"EXPORTED":        "yes",
		"QUOTED":          "hello world",
		"SINGLE":          "raw $NOT_EXPANDED",
		"EMPTY":           "",
		"SPACED":          "trimmed",
		"EQUALS_IN_VALUE": "a=b=c",
		"ESCAPED":         "line\nbreak",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d keys, want %d: %#v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q, want %q", k, got[k], v)
		}
	}
}

// No interpolation is a deliberate choice, not an oversight: substitution syntax
// is a mechanism, and gagarin adds capabilities rather than mechanisms.
func TestParseEnvFileDoesNotInterpolate(t *testing.T) {
	p := writeTemp(t, ".env", "A=1\nB=${A}\nC=$A\n")
	got, err := parseEnvFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if got["B"] != "${A}" {
		t.Errorf("B: got %q, want literal ${A}", got["B"])
	}
	if got["C"] != "$A" {
		t.Errorf("C: got %q, want literal $A", got["C"])
	}
}

func TestParseEnvFileRejectsBadInput(t *testing.T) {
	for name, content := range map[string]string{
		"no equals":     "JUST_A_KEY\n",
		"empty key":     "=value\n",
		"invalid name":  "not-a-valid-name=1\n",
		"leading digit": "1BAD=1\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseEnvFile(writeTemp(t, ".env", content)); err == nil {
				t.Errorf("expected an error for %q", content)
			}
		})
	}
}

func TestParseEnvFileMissing(t *testing.T) {
	if _, err := parseEnvFile(filepath.Join(t.TempDir(), "nope.env")); err == nil {
		t.Error("expected an error for a missing file")
	}
}

// Precedence must not depend on the order flags appear on the command line.
func TestDeployEnvPrecedence(t *testing.T) {
	base := writeTemp(t, "base.env", "SHARED=from-base\nONLY_BASE=b\n")
	over := writeTemp(t, "over.env", "SHARED=from-over\nONLY_OVER=o\n")

	for _, args := range [][]string{
		{"-env-file", base, "-env-file", over, "-env", "SHARED=from-flag"},
		{"-env", "SHARED=from-flag", "-env-file", base, "-env-file", over},
	} {
		f, err := parseDeploy(args)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if f.env["SHARED"] != "from-flag" {
			t.Errorf("%v: SHARED = %q, want from-flag", args, f.env["SHARED"])
		}
		if f.env["ONLY_BASE"] != "b" || f.env["ONLY_OVER"] != "o" {
			t.Errorf("%v: lost a key: %#v", args, f.env)
		}
	}
}

func TestDeployLaterEnvFileWins(t *testing.T) {
	a := writeTemp(t, "a.env", "K=first\n")
	b := writeTemp(t, "b.env", "K=second\n")
	f, err := parseDeploy([]string{"-env-file", a, "-env-file", b})
	if err != nil {
		t.Fatal(err)
	}
	if f.env["K"] != "second" {
		t.Errorf("K = %q, want second", f.env["K"])
	}
}

// A missing env file must fail loudly. Silently deploying without the variables
// an app needs produces a service that starts and then misbehaves, which is far
// harder to diagnose than a refusal.
func TestDeployMissingEnvFileIsAnError(t *testing.T) {
	if _, err := parseDeploy([]string{"-env-file", "/nonexistent/.env"}); err == nil {
		t.Error("expected deploy parsing to fail on a missing env file")
	}
}
