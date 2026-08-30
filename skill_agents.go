package main

import "path/filepath"

// agentTarget is one coding-agent harness gg knows how to install its skill
// into: a key for --agent, a name to print, and the directory that harness
// reads global skills from. gg writes gagarin/SKILL.md underneath it, the
// same layout Claude Code itself uses.
type agentTarget struct {
	key, displayName string
	homeRelDir       string
}

// agentTargets is necessarily incomplete: there is no registry of every agent
// harness in existence, and a wrong guess here is worse than an omission.
// Claude Code is first and is what a bare `gg skill install` still installs,
// matching the behaviour this had before --agent existed.
var agentTargets = []agentTarget{
	{"claude", "Claude Code", filepath.Join(".claude", "skills")},
	{"cursor", "Cursor", filepath.Join(".cursor", "skills")},
	{"windsurf", "Windsurf", filepath.Join(".codeium", "windsurf", "skills")},
	{"cline", "Cline", filepath.Join(".cline", "skills")},
	{"codex", "Codex CLI", filepath.Join(".codex", "skills")},
	{"copilot", "GitHub Copilot", filepath.Join(".copilot", "skills")},
	{"continue", "Continue.dev", filepath.Join(".continue", "skills")},
}

func findAgentTarget(key string) (agentTarget, bool) {
	for _, a := range agentTargets {
		if a.key == key {
			return a, true
		}
	}
	return agentTarget{}, false
}

func agentKeys() []string {
	keys := make([]string, len(agentTargets))
	for i, a := range agentTargets {
		keys[i] = a.key
	}
	return keys
}
