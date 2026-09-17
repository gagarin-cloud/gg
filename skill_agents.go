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
//
// `agentskills` is second because it is the one that stops this list growing:
// the Agent Skills standard (agentskills.io) names ~/.agents/skills as the path
// a client reads for skills it was not built with, and Cursor, Codex, Copilot,
// Cline, Windsurf, Goose, opencode and Zed all honour it. Prefer it to adding
// a row here; a row is only worth it for a harness that reads nothing else.
//
// Every path below is checked against that harness's own documentation or
// source. An entry nobody reads is worse than a missing one, because the
// install prints success either way — which is exactly what `codex` used to
// do: ~/.codex/skills was real in late 2025 behind `codex --enable skills`,
// and OpenAI has since moved Codex to ~/.agents/skills, so the two share a
// destination now.
var agentTargets = []agentTarget{
	{"claude", "Claude Code", filepath.Join(".claude", "skills")},
	{"agentskills", "any Agent Skills client", filepath.Join(".agents", "skills")},
	{"cursor", "Cursor", filepath.Join(".cursor", "skills")},
	{"windsurf", "Windsurf", filepath.Join(".codeium", "windsurf", "skills")},
	{"cline", "Cline", filepath.Join(".cline", "skills")},
	{"codex", "Codex CLI", filepath.Join(".agents", "skills")},
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

// dedupeAgentKeys drops keys that would write the file a second time to a path
// an earlier key already covered. Two harnesses can share a directory — codex
// reads the same ~/.agents/skills that `agentskills` names — and `--agent all`
// should not report installing the same file twice under two different names.
func dedupeAgentKeys(keys []string) []string {
	seen := make(map[string]bool, len(keys))
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		agent, ok := findAgentTarget(key)
		if !ok {
			// Leave it in: installSkillForAgent owns the error message.
			out = append(out, key)
			continue
		}
		if seen[agent.homeRelDir] {
			continue
		}
		seen[agent.homeRelDir] = true
		out = append(out, key)
	}
	return out
}
