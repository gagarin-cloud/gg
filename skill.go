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

// skillTargets maps an agent harness to where it keeps its skills. Claude Code
// is the only one with a stable convention today; the map exists so adding the
// next one is a line rather than a redesign.
var skillTargets = map[string]string{
	"claude": filepath.Join(".claude", "skills", "gagarin", "SKILL.md"),
}

func cmdSkillShow() error {
	fmt.Print(skillMarkdown)
	return nil
}

func installSkill(dir string) error {
	path := dir
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cannot find your home directory: %w", err)
		}
		path = filepath.Join(home, skillTargets["claude"])
	} else if !strings.HasSuffix(path, ".md") {
		// A directory was given: put the file in it under its conventional name.
		path = filepath.Join(path, "SKILL.md")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Overwriting is the point: an upgraded gg carries an updated skill, and a
	// stale copy teaching an older flow is worse than none.
	existed := false
	if _, err := os.Stat(path); err == nil {
		existed = true
	}
	if err := os.WriteFile(path, []byte(skillMarkdown), 0o644); err != nil {
		return err
	}

	verb := "installed"
	if existed {
		verb = "updated"
	}
	fmt.Printf("%s the gagarin skill at %s\n", verb, path)
	fmt.Printf("your agent will pick it up on its next session\n")
	return nil
}
