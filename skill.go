package main

// The agent skill ships inside the binary.
//
// gagarin's whole premise is that the agent does the work, which only holds if
// the agent can find out how. That knowledge lives in SKILL.md, and the CLI is
// the one thing every user installs — so the skill rides along with it rather
// than living on a website an agent has to be told to read.
//
// It is a separate command rather than something `go install` could do on its
// own: installing a Go binary runs no hooks, so a claim that installation also
// installs the skill would be false. One explicit command is the honest version.

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed skill/SKILL.md
var skillMarkdown string

func cmdSkillShow() error {
	fmt.Print(skillMarkdown)
	return nil
}

// skillDirName is the directory a SKILL.md must sit in. The Agent Skills
// standard requires it to equal the skill's frontmatter `name`, and clients
// that disagree with it skip the skill without saying so — so this constant
// and the `name:` at the top of skill/SKILL.md are one fact in two places.
const skillDirName = "gagarin"

// installSkill writes SKILL.md to an explicit path (or into it, if dir names
// a directory rather than ending in .md). This is the --dir escape hatch: a
// single destination, not a choice among known agents.
func installSkill(dir string) error {
	path := dir
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cannot find your home directory: %w", err)
		}
		claude, ok := findAgentTarget("claude")
		if !ok {
			// Unreachable while the table has a claude row, and a silent
			// ~/gagarin/SKILL.md is the wrong way to find out it does not.
			return fmt.Errorf("no install location for Claude Code; name one with --dir")
		}
		path = filepath.Join(home, claude.homeRelDir, skillDirName, "SKILL.md")
	} else if !strings.HasSuffix(path, ".md") {
		// A directory was given. Nest under gagarin/ unless the directory is
		// already named that: a skill whose parent directory does not match its
		// `name` is skipped by most clients, so writing SKILL.md straight into
		// a skills root — the obvious thing to type — would install nothing
		// while reporting success.
		if filepath.Base(filepath.Clean(path)) != skillDirName {
			path = filepath.Join(path, skillDirName)
		}
		path = filepath.Join(path, "SKILL.md")
	}
	return writeSkillFile(path, "")
}

// installSkillForAgent writes SKILL.md into the given harness's conventional
// skills directory under the user's home.
func installSkillForAgent(key string) error {
	agent, ok := findAgentTarget(key)
	if !ok {
		return fmt.Errorf("unknown agent %q — known agents: %s", key, strings.Join(agentKeys(), ", "))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot find your home directory: %w", err)
	}
	path := filepath.Join(home, agent.homeRelDir, skillDirName, "SKILL.md")
	return writeSkillFile(path, agent.displayName)
}

// writeSkillFile writes the embedded skill to path, printing what happened.
// label, if non-empty, names the agent the message is about.
func writeSkillFile(path, label string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Overwriting is the point: an upgraded gg carries an updated skill, and a
	// stale copy teaching an older flow is worse than none.
	existed := false
	if _, err := os.Stat(path); err == nil {
		existed = true
	}
	// Write-then-rename: a half-written SKILL.md is a skill that teaches half
	// a flow, and an interrupted install should leave the previous one intact
	// rather than a truncated file that still parses.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".SKILL.md.*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(skillMarkdown); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}

	verb := "installed"
	if existed {
		verb = "updated"
	}
	if label != "" {
		fmt.Printf("%s the gagarin skill for %s at %s\n", verb, label, path)
	} else {
		fmt.Printf("%s the gagarin skill at %s\n", verb, path)
	}
	return nil
}
