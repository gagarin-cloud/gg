package main

import (
	"testing"
)

func TestSplitSourceTakesTheImagesOwnName(t *testing.T) {
	for _, tc := range []struct{ source, repo, tag string }{
		{"postgres:17-alpine", "postgres", "17-alpine"},
		{"postgres", "postgres", "latest"},
		{"library/postgres:17", "postgres", "17"},
		{"docker.io/redis:7-alpine", "redis", "7-alpine"},
		// A colon before the last slash is a port, not a tag.
		{"localhost:5000/thing:v2", "thing", "v2"},
		{"localhost:5000/thing", "thing", "latest"},
	} {
		repo, tag, err := splitSource(tc.source, "")
		if err != nil {
			t.Errorf("%q: unexpected error: %v", tc.source, err)
			continue
		}
		if repo != tc.repo || tag != tc.tag {
			t.Errorf("%q: got %q:%q, want %q:%q", tc.source, repo, tag, tc.repo, tc.tag)
		}
	}
}

// A project's space has room for one path segment, because the segment before it
// is the project id and that is the tenant boundary. Where flattening would be a
// guess, say so and show the fix rather than inventing a rule people have to
// learn by being surprised.
func TestSplitSourceAsksRatherThanGuessesAtDeepPaths(t *testing.T) {
	_, _, err := splitSource("ghcr.io/org/tool:v1", "")
	if err == nil {
		t.Fatal("a three-segment path should not be flattened silently")
	}
	if got := err.Error(); !contains(got, "-name tool") {
		t.Errorf("the error should show the fix, got: %s", got)
	}

	repo, tag, err := splitSource("ghcr.io/org/tool:v1", "tool")
	if err != nil {
		t.Fatalf("with -name it should work: %v", err)
	}
	if repo != "tool" || tag != "v1" {
		t.Errorf("got %q:%q, want tool:v1", repo, tag)
	}
}

// gagarin runs images by digest and copying one platform out of a
// multi-architecture image produces a different one, so a source digest cannot
// be carried across. Refusing is honest; re-tagging it would not be.
func TestSplitSourceRefusesADigest(t *testing.T) {
	_, _, err := splitSource("postgres@sha256:"+repeat("a", 64), "")
	if err == nil {
		t.Fatal("copying by digest should be refused")
	}
}

func TestSplitSourceRefusesNamesTheRegistryWouldNot(t *testing.T) {
	for _, name := range []string{"UPPER", "has space", "two/segments", "-leading"} {
		if _, _, err := splitSource("postgres:17", name); err == nil {
			t.Errorf("%q should be refused as a repository name", name)
		}
	}
	// An empty -name is not a bad name, it is an absent one: the flag defaults to
	// empty and the image's own name is used.
	if repo, _, err := splitSource("postgres:17", ""); err != nil || repo != "postgres" {
		t.Errorf(`-name "" should fall back to the image's name, got %q (%v)`, repo, err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func repeat(s string, n int) string {
	out := ""
	for range n {
		out += s
	}
	return out
}
