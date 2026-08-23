# gg — the gagarin CLI

`gg` deploys and operates applications on [gagarin](https://gagarin.cloud), a
cloud your coding agent can drive and you can read.

It is a thin wrapper over the gagarin API and holds no state of its own beyond a
credential file. Anything `gg` can do, the API can do — there is no second path,
and nothing about your deployment lives in your repository.

## Install

```
go install github.com/gagarin-cloud/gg@latest
```

Or take a binary for your platform from
[releases](https://github.com/gagarin-cloud/gg/releases). Every release publishes
checksums; verify them. There is deliberately no `curl | bash` one-liner.

Then, if you use an agent:

```
gg skill install
```

That writes the agent skill to `~/.claude/skills/gagarin/`, which is how a coding
agent learns to use gagarin without you explaining it. The skill ships inside this
binary, so it never disagrees with the CLI you have.

## Use

```
gg signup you@example.com      # a human clicks a button in an email; that is the whole signup
gg auth --claim ABCD-1234      # waits for that click, stores credentials
gg init myapp                  # create a project
gg deploy --port 8080          # build, push, run the current directory
gg resource add postgres db    # a database; you choose the name and the size, nothing else
gg resource secrets db         # its credentials, to pass to a deploy like any other env
gg status                      # what gagarin intends, and what the cluster is actually doing
gg logs web
gg share teammate@example.com  # editors deploy and manage; viewers read
gg destroy myapp               # asks your human, every time
```

`gg help` lists everything.

## Two things worth knowing

**Your agent can ship, but it cannot delete.** The credential a single email click
produces can deploy, read status and read logs. Destroying anything answers
`approval_required` and emails you a button — every time the approval window has
lapsed. An agent cannot grant itself that capability by asking.

**Errors are meant to be acted on.** Every failure carries a stable `code`, a
message, and a `fix_hint`. Agents should branch on the code, not the prose.

## Building from source

```
go build -o gg .
go test ./...
```

No code generation, no build tags, no vendored tree.

## Licence

MIT. See [LICENSE](LICENSE).
