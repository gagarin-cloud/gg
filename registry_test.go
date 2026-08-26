package main

import "testing"

// The tag comes off the source when the destination does not give one. The
// source is not a gagarin reference — it can carry a registry host and any
// number of path segments — so it is read rather than parsed.
func TestSourceTagReadsTheTagOffAnyReference(t *testing.T) {
	for in, want := range map[string]string{
		"postgres:17-alpine":     "17-alpine",
		"postgres":               "latest",
		"library/postgres:17":    "17",
		"docker.io/redis:7-alpi": "7-alpi",
		"ghcr.io/org/tool:v1":    "v1",
		"ghcr.io/org/tool":       "latest",
		// A colon before the last slash is a port, not a tag.
		"localhost:5000/thing:v2": "v2",
		"localhost:5000/thing":    "latest",
	} {
		t.Run(in, func(t *testing.T) {
			got, err := sourceTag(in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

// gagarin runs images by digest, and copying one platform out of a
// multi-architecture image produces a different one — so a source digest cannot
// be carried across, and saying so beats producing something confusing.
func TestSourceTagRefusesADigest(t *testing.T) {
	digest := "postgres@sha256:"
	for range 64 {
		digest += "a"
	}
	if _, err := sourceTag(digest); err == nil {
		t.Error("expected a digest-pinned source to be refused")
	}
}

func TestSourceTagRefusesWhatARegistryWould(t *testing.T) {
	for name, in := range map[string]string{
		"empty":     "",
		"bad tag":   "postgres:-leading",
		"empty tag": "postgres:",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := sourceTag(in); err == nil {
				t.Errorf("expected %q to be refused", in)
			}
		})
	}
}
