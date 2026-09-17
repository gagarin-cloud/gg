# gg — the gagarin CLI

`gg` deploys and operates applications on [gagarin](https://gagarin.cloud), a
cloud your coding agent can drive and you can read.

It is a thin wrapper over the gagarin API and holds no state of its own beyond a
credential file. Anything `gg` can do, the API can do — there is no second path,
and nothing about your deployment lives in your repository.

## Install

```
brew install gagarin-cloud/tap/gg
```

Or, with a Go toolchain:

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
gg login                       # prints a link and a code for your human to open and approve
gg login                       # again, once they have: stores credentials, logs docker in

gg creds                                     # what has access to this account
gg creds create --name "github actions"      # mint one for CI: deploy-only, expiring, printed once
gg creds revoke 7                            # take one away
gg init shop                   # create a project

gg ship shop/web:8080          # build the current directory, push it, run it
gg run shop/migrate migrate:v3 # run an image to completion, wait, exit with its code
gg domain add shop/web         # put it on the internet, at an address gagarin gives you
gg status shop                 # what gagarin intends, and what the cluster is actually doing
gg logs shop/web

gg resource add shop/db postgres    # a database; you choose the name and the size, nothing else
gg resource add shop/cache valkey   # postgres, qdrant and valkey — valkey is in-memory and loses data on restart
gg deps add shop/web db             # let web reach it, and hand it DB_URL and the rest
gg resource secrets shop/db         # the values, when something outside the project needs them
gg connect shop/db                  # that database on 127.0.0.1, for as long as the command runs

gg resource add shop/openai external --env-file .env.openai   # a third-party key as a node on the graph
gg deps add shop/bot openai         # bot now holds OPENAI_API_KEY
gg resource rotate shop/openai --env-file .env.new   # new key, and every holder rolls
gg resource rotate shop/db          # a database: gagarin mints it, no downtime

gg domain add shop/web shop.example.com   # also answer on a name you own; prints the DNS record to create
gg share shop teammate@example.com        # editors deploy and manage; viewers read
gg transfer shop teammate@example.com     # offer them the project, and its bill; they accept by email
gg destroy shop                           # asks your human, every time
```

Build, push and deploy are also separate, for when you want less than all three
— CI publishing on every commit and releasing on some of them, or running an
image you did not just build:

```
gg build  shop/web:v3 --context ./web    # make an image, run nothing
gg push   shop/web:v3                    # publish it, release nothing
gg deploy shop/web:8080 web:v3           # release one that already exists
```

`gg login` is the same command every time — first machine or fifth, new account
or old. It prints a link and a code. A human opens the link, signs in with
GitHub or Google, checks that the page shows the same code and the name of the
machine asking, and approves; running `gg login` again then stores the
credential and logs docker in. At a terminal it simply waits for the approval
instead, and `gg login --new` throws away a request nobody approved.
The first sign-in creates the account with $5 on it; no card is asked for. Never
run it from CI: a pipeline gets its own credential from `gg creds create`, run by
a human on a machine that already has one.

`gg help` lists everything.

## Four things worth knowing

**Nothing is inferred from your working directory.** Every command names the
project it acts on: `shop` for the project, `shop/web` for a service in it. A
command that does not is refused, with the shape it should have had. There is no
default to configure and no state file in your repo.

**Your agent can ship, but it cannot take anything away.** The credential
`gg login` produces can deploy, read status and read logs. Destroying anything,
releasing an address and withdrawing a dependency all answer `approval_required`
and email you a button — every time the approval window has lapsed — and the
button approves only once you are signed in as the account it was sent for. An
agent cannot grant itself that capability by asking. Adding is free in every case: it
is the taking away that can break something.

**Errors are meant to be acted on.** Every failure carries a stable `code`, a
message, and a `fix_hint`. Agents should branch on the code, not the prose.

**A deploy changes the image and the environment.** What a service is allowed to
reach (`gg deps`), the addresses it answers on (`gg domain`) and the volume it
keeps are declared separately and survive every deploy — none of them can be
released by a deploy that forgets to restate it. That includes being on the
internet at all: `gg ship` can neither give a service an address nor take one
away, and taking one away asks a human first. So does `gg deps rm`, for a harder
version of the same reason: a withdrawn edge produces no error anywhere — the
calls are dropped, so the caller hangs until it gives up.

`gg deploy --deps db` and `gg ship --deps db` are the one exception, and only in
the direction that cannot lose anything: they add to what a service may reach,
so that a service needing a database can be declared and deployed in one call
instead of starting into a window where it cannot reach one. Withdrawing is
`gg deps rm`, and a deploy that omits `--deps` changes the graph not at all.

**Declaring a dependency on a resource hands over its credentials.** `gg deps add
shop/web db` opens the route and puts `DB_URL`, `DB_HOST`, `DB_PORT`, `DB_USER`,
`DB_PASSWORD` and `DB_DATABASE` in `web`'s environment — named after the
resource, so a service can reach two databases without a collision. The platform
derives them from the graph every time it starts the pod rather than copying them
into the service's stored environment, so they cannot go stale and no redeploy
can forget them. `gg resource secrets` prints the values for anything outside the
project that needs them.

## Building from source

```
go build -o gg .
go test ./...
```

No code generation, no build tags, no vendored tree.

## Licence

MIT. See [LICENSE](LICENSE).
