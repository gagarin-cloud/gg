package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every target must name a directory, and no target may point outside the
// user's home: installSkillForAgent joins these onto os.UserHomeDir().
func TestAgentTargetsAreWellFormed(t *testing.T) {
	seenKeys := map[string]bool{}
	for _, a := range agentTargets {
		if a.key == "" || a.displayName == "" || a.homeRelDir == "" {
			t.Errorf("agent %+v has an empty field", a)
		}
		if seenKeys[a.key] {
			t.Errorf("duplicate --agent key %q", a.key)
		}
		seenKeys[a.key] = true
		if filepath.IsAbs(a.homeRelDir) || strings.Contains(a.homeRelDir, "..") {
			t.Errorf("agent %q: homeRelDir %q must stay under the home directory", a.key, a.homeRelDir)
		}
		if !strings.HasSuffix(a.homeRelDir, "skills") {
			t.Errorf("agent %q: homeRelDir %q does not end in a skills directory", a.key, a.homeRelDir)
		}
	}
	if _, ok := findAgentTarget("all"); ok {
		t.Error(`"all" is a --agent keyword and must not also be a target key`)
	}
}

// "all" must not report installing the same file twice. codex and agents share
// ~/.agents/skills, so the loop has to collapse them.
func TestDedupeAgentKeysCollapsesSharedDirectories(t *testing.T) {
	codex, ok := findAgentTarget("codex")
	if !ok {
		t.Fatal("codex target missing")
	}
	agents, ok := findAgentTarget("agentskills")
	if !ok {
		t.Fatal("agentskills target missing")
	}
	if codex.homeRelDir != agents.homeRelDir {
		t.Fatalf("expected codex to read the Agent Skills path; got %q and %q",
			codex.homeRelDir, agents.homeRelDir)
	}

	got := dedupeAgentKeys(agentKeys())
	dirs := map[string]bool{}
	for _, key := range got {
		a, _ := findAgentTarget(key)
		if dirs[a.homeRelDir] {
			t.Errorf("dedupeAgentKeys left two keys writing %q", a.homeRelDir)
		}
		dirs[a.homeRelDir] = true
	}
	if len(got) >= len(agentKeys()) {
		t.Errorf("expected dedupe to drop at least one key, got %v", got)
	}
	// The first key naming a directory is the one that survives, so the
	// message a user sees for ~/.agents/skills stays the cross-client one.
	if !contains(got, "agentskills") || contains(got, "codex") {
		t.Errorf("expected 'agentskills' to win over 'codex'; got %v", got)
	}
}

// An unknown key must survive dedupe so installSkillForAgent can report it.
func TestDedupeAgentKeysKeepsUnknownKeys(t *testing.T) {
	got := dedupeAgentKeys([]string{"claude", "nonesuch"})
	if !contains(got, "nonesuch") {
		t.Errorf("unknown key was dropped before it could be reported: %v", got)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// A skill is only discovered when its parent directory matches the frontmatter
// `name`. --dir naming a skills root must therefore nest, or it installs
// nothing while printing success.
func TestInstallSkillDirNestsUnderSkillName(t *testing.T) {
	for _, tc := range []struct{ name, give, want string }{
		{"skills root nests", "skills", filepath.Join("skills", skillDirName, "SKILL.md")},
		{"already named gagarin", skillDirName, filepath.Join(skillDirName, "SKILL.md")},
		{"trailing separator", "skills" + string(filepath.Separator), filepath.Join("skills", skillDirName, "SKILL.md")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := installSkill(filepath.Join(root, tc.give)); err != nil {
				t.Fatalf("installSkill: %v", err)
			}
			want := filepath.Join(root, tc.want)
			got, err := os.ReadFile(want)
			if err != nil {
				t.Fatalf("expected the skill at %s: %v", want, err)
			}
			if string(got) != skillMarkdown {
				t.Error("installed skill does not match the embedded one")
			}
		})
	}
}

// An explicit .md path stays literal — that is what the escape hatch is for.
func TestInstallSkillHonoursExplicitFile(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, "nested", "whatever.md")
	if err := installSkill(want); err != nil {
		t.Fatalf("installSkill: %v", err)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected the skill at %s: %v", want, err)
	}
}

// The directory a skill is written into must equal its frontmatter name, or
// clients skip it. These are one fact in two files; keep them in step.
func TestSkillDirNameMatchesFrontmatter(t *testing.T) {
	var name string
	for _, line := range strings.Split(skillMarkdown, "\n") {
		if strings.HasPrefix(line, "name:") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
			break
		}
	}
	if name != skillDirName {
		t.Errorf("SKILL.md frontmatter name %q != skillDirName %q", name, skillDirName)
	}
}

// Overwriting must not leave a truncated file behind.
func TestWriteSkillFileReplacesAtomically(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "SKILL.md")
	if err := os.WriteFile(path, []byte("stale, and much longer than nothing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeSkillFile(path, ""); err != nil {
		t.Fatalf("writeSkillFile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != skillMarkdown {
		t.Error("skill was not replaced with the embedded copy")
	}
	// The temp file must not survive next to the real one.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected only SKILL.md, found %d entries", len(entries))
	}
}
