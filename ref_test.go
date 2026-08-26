package main

import (
	"strings"
	"testing"
)

func TestParseServiceTakesProjectNameAndPort(t *testing.T) {
	for in, want := range map[string]struct {
		project, name string
		port          int
	}{
		"shop/web":         {"shop", "web", 0},
		"shop/web:8080":    {"shop", "web", 8080},
		"shop/web:1":       {"shop", "web", 1},
		"shop/web:65535":   {"shop", "web", 65535},
		"a1/b2-c3:5432":    {"a1", "b2-c3", 5432},
		"shop/web-api:300": {"shop", "web-api", 300},
	} {
		t.Run(in, func(t *testing.T) {
			project, name, port, err := parseService(in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if project != want.project || name != want.name || port != want.port {
				t.Errorf("got %q/%q:%d, want %q/%q:%d",
					project, name, port, want.project, want.name, want.port)
			}
		})
	}
}

// The refusal somebody upgrading from the old CLI meets first, and the one an
// agent carrying a stale skill meets. It has to teach the shape, not just say no
// — being told "web is not valid" leaves you no better off than before.
func TestParseServiceTeachesTheShapeForABareName(t *testing.T) {
	_, _, _, err := parseService("web")
	if err == nil {
		t.Fatal("expected a bare service name to be refused")
	}
	if !strings.Contains(err.Error(), "<project>/web") {
		t.Errorf("the error must show the fix, got: %v", err)
	}
}

func TestParseServiceRefusesWhatTheControlPlaneWould(t *testing.T) {
	for name, in := range map[string]string{
		"empty":               "",
		"project only":        "shop",
		"trailing slash":      "shop/",
		"leading slash":       "/web",
		"three parts":         "shop/web/extra",
		"uppercase project":   "Shop/web",
		"uppercase service":   "shop/Web",
		"project leading dig": "1shop/web",
		"service leading dig": "shop/1web",
		"underscore":          "shop/web_api",
		"one letter service":  "shop/w",
		"port not a number":   "shop/web:http",
		"port zero":           "shop/web:0",
		"port too large":      "shop/web:65536",
		"port negative":       "shop/web:-1",
		"empty port":          "shop/web:",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, _, err := parseService(in); err == nil {
				t.Errorf("expected %q to be refused", in)
			}
		})
	}
}

// Thirty characters is the control plane's limit, so it is this one's too — a
// name that parses here and is refused there is worse than no check at all.
func TestParseServiceRefusesNamesTooLongForAHostname(t *testing.T) {
	ok := strings.Repeat("a", 30)
	if _, _, _, err := parseService("shop/" + ok); err != nil {
		t.Errorf("30 characters is allowed, got: %v", err)
	}
	if _, _, _, err := parseService("shop/" + strings.Repeat("a", 31)); err == nil {
		t.Error("31 characters must be refused")
	}
	if _, _, _, err := parseService(strings.Repeat("a", 31) + "/web"); err == nil {
		t.Error("a 31-character project must be refused")
	}
}

func TestParseImageTakesProjectRepoAndTag(t *testing.T) {
	for in, want := range map[string][3]string{
		"shop/api:v3":         {"shop", "api", "v3"},
		"shop/api":            {"shop", "api", ""},
		"shop/api:latest":     {"shop", "api", "latest"},
		"shop/my-tool:1.2.3":  {"shop", "my-tool", "1.2.3"},
		"shop/tool_x:v1":      {"shop", "tool_x", "v1"},
		"shop/api:1730000000": {"shop", "api", "1730000000"},
		// Docker allows an uppercase tag even though it does not allow an
		// uppercase repository, and it is not gg's place to be stricter.
		"shop/api:V3": {"shop", "api", "V3"},
	} {
		t.Run(in, func(t *testing.T) {
			project, repo, tag, err := parseImage(in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := [3]string{project, repo, tag}; got != want {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	}
}

// An image reference is to gagarin's own registry and nothing else. A host in
// front of it is somebody reaching for Docker Hub, which is `gg registry copy`
// — so it has to be refused rather than silently read as a project name.
func TestParseImageRefusesAReferenceToSomebodyElsesRegistry(t *testing.T) {
	for _, in := range []string{
		"docker.io/library/postgres:17",
		"ghcr.io/org/tool:v1",
		"localhost:5000/thing:v2",
		"registry.gagarin.cloud/a1b2c3d4/api:v3",
	} {
		t.Run(in, func(t *testing.T) {
			if _, _, _, err := parseImage(in); err == nil {
				t.Errorf("expected %q to be refused", in)
			}
		})
	}
}

func TestParseImageRefusesUnusableNames(t *testing.T) {
	for name, in := range map[string]string{
		"empty":       "",
		"no project":  "api",
		"upper repo":  "shop/API",
		"space":       "shop/my api",
		"bad tag":     "shop/api:-leading",
		"empty tag":   "shop/api:",
		"digest form": "shop/api@sha256:" + strings.Repeat("a", 64),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, _, err := parseImage(in); err == nil {
				t.Errorf("expected %q to be refused", in)
			}
		})
	}
}

func TestParseProjectTakesABareName(t *testing.T) {
	got, err := parseProject("shop")
	if err != nil || got != "shop" {
		t.Fatalf("got %q, %v", got, err)
	}
}

// A project argument given a service reference is somebody who has the two
// confused, and the fix is the half of what they typed that is actually a
// project.
func TestParseProjectRefusesAServiceReferenceAndSaysWhatItWanted(t *testing.T) {
	_, err := parseProject("shop/web")
	if err == nil {
		t.Fatal("expected shop/web to be refused as a project")
	}
	if !strings.Contains(err.Error(), "shop") {
		t.Errorf("the error must name the project it found, got: %v", err)
	}
}

func TestParseProjectRefusesUnusableNames(t *testing.T) {
	for name, in := range map[string]string{
		"empty":                         "",
		"uppercase":                     "Shop",
		"leading digit":                 "1shop",
		"one letter":                    "s",
		"underscore":                    "my_shop",
		"trailing dash but valid chars": strings.Repeat("a", 31),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseProject(in); err == nil {
				t.Errorf("expected %q to be refused", in)
			}
		})
	}
}
