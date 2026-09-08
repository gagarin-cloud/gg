---
name: gagarin
description: Deploy and operate applications on gagarin with the gg CLI — ship a service from a Dockerfile, provision postgres, qdrant, valkey or external-credential resources, wire what may reach what, put a service on the internet, give CI its own credential, read status and logs, roll back, and tear things down. Use whenever the user asks to deploy, host, run or operate an application, or mentions gagarin or gg. Gagarin runs container images on managed infrastructure; the user never needs to know about Kubernetes, ingress, TLS, or the underlying cloud.
---

# Deploying with gagarin

Gagarin runs your services. You drive it with the `gg` CLI, a thin wrapper over
the gagarin API. There is exactly one way to change anything: call the API.
There is no manifest file, no config file, and nothing to commit.

Everything below is the whole product. Read "The ten rules" and the command map
first; they are the parts that stop you getting it wrong.

## The ten rules

1. **Everything is named for its project.** `shop` is a project; `shop/web` is a
   service or a resource inside it. Nothing is inferred from the directory you
   stand in. There is no default project to configure — a command that does not
   name one is refused with the shape it should have had.
2. **Every write is asynchronous, except `gg run`.** A command that exits zero
   recorded a demand; it did not watch it come true. `gg status <project>` reads
   the cluster and is the only thing that can answer "is it up". The one
   exception is `gg run`, and it is an exception because a run *ends*: it waits,
   prints what the run wrote, and exits with the script's own code. Nothing else
   here has an end to wait for.
3. **Gagarin runs images from its own registry only.** `gg ship` is build, push
   and deploy fused; the three exist separately for CI. Somebody else's image
   comes in with `gg registry copy`.
4. **A service is private until `gg domain add`**, and a deploy can neither give
   an address nor take one away.
5. **A private service is default-denied.** Until the *caller* declares
   `gg deps add`, its calls are dropped — which hangs, rather than failing fast.
6. **`gg deps add <service> <resource>` opens the route *and* hands over the
   credentials.** Connecting a database is that one call, not a call plus a
   deploy.
7. **A deploy replaces the environment and nothing else.** Dependencies, the
   domain, the volume and the size all survive a deploy that forgets to mention
   them. Env is the one thing you must restate every time.
8. **If a resource type exists, use it** (`postgres`, `qdrant`, `valkey`,
   `external`). If one does not, an ordinary service with a volume is the normal
   path, not a workaround.
9. **You can deploy; you cannot destroy.** Deleting anything — and handing a
   project over, which is `gg transfer` — answers `approval_required` and emails
   the account owner a button, every time the approval window has lapsed. No flag
   grants this to an agent.
10. **Errors are meant to be branched on.** gg prints `[code] message` and
    usually a `hint:` line. Act on the code, never on the prose.

## Command map

| | |
|---|---|
| `gg whoami` | which account this machine acts as — **run this first, always** |
| `gg signup EMAIL` / `gg auth --claim CODE` | get this machine access |
| `gg creds` / `creds create --name N` / `creds revoke ID` | what has access; mint one for CI; take one away |
| `gg registry login` | log docker in (CI, or docker installed after gg) |
| `gg projects` | every project you can reach, and your role on it |
| `gg init PROJECT` | create a project |
| `gg ship P/SVC:PORT` | build the current directory, push it, run it |
| `gg build P/IMAGE[:TAG]` / `gg push P/IMAGE:TAG` / `gg deploy P/SVC:PORT IMAGE:TAG` | the same three steps apart, which is what CI wants |
| `gg run P/JOB IMAGE:TAG` | run an image to completion as a job, wait, print what it wrote, exit with its code |
| `gg registry copy P/IMAGE SOURCE` | bring an image you did not build into the project |
| `gg resource add P/NAME TYPE` | provision postgres, qdrant, valkey or external |
| `gg resource secrets P/NAME` | its connection values, for something outside the project |
| `gg resource secrets P/NAME --names` | just the variable names it publishes, no values |
| `gg resource rotate P/NAME` | new credentials, and everything holding them rolls |
| `gg resource rotate P/NAME --set K=V` | change one value an external publishes, keeping the rest |
| `gg rollback P/NAME` | put a previous revision back — a service's deploy, or an external's values |
| `gg connect P/NAME` | that resource on this machine, for as long as the command runs |
| `gg resource backup` / `backups` / `restore P/NEW --source OLD` | postgres recovery |
| `gg deps add P/SVC NAME...` / `deps ls` / `deps rm` | what a service may reach, and whose credentials it holds |
| `gg domain add P/SVC [DOMAIN]` / `domain ls P` / `domain rm` | addresses on the internet |
| `gg status PROJECT` | desired vs actual, addresses, sizes, today's cost |
| `gg logs P/SVC` | recent logs |
| `gg history P/SVC` / `gg rollback P/SVC [--to N]` | every deploy; put one back |
| `gg members P` / `gg share P EMAIL [--role viewer]` / `gg unshare P EMAIL` | who can reach it |
| `gg transfer P EMAIL` | offer the project, and its bill, to a member (they accept by email) |
| `gg destroy P` or `P/NAME` | delete a project, a service or a resource (needs a human) |
| `gg eject P -o file.yaml` | the Kubernetes manifests, so you can leave (external keys are placeholders; `--with-secrets` includes them) |
| `gg skill install` | refresh this skill from the binary |
| `gg version` | which gg this is |

Three environment variables override the credential file, and exist for CI:
`GAGARIN_TOKEN` (a credential), `GAGARIN_API` (control plane URL), and
`GAGARIN_REGISTRY` (registry host). There is nothing else to export.

## First moves in any session

Run `gg whoami` before anything else. It answers three different questions at
once, and each has a different next step:

| what happens | what it means | do this |
|---|---|---|
| the shell cannot find `gg` | not installed | see "Installing gg" |
| `[unauthorized]`, or it says there are no credentials | installed, no access | see "Getting access" |
| it names an account | ready | carry on; `gg projects` says what already exists |

Then `gg projects` before assuming a project exists or that you may write to it.
A `viewer` role means every deploy will be refused, and that is worth knowing
before the attempt rather than after. Project names are unique only within one
account, so two rows can share a name — the id column tells them apart.

## Installing gg

Prefer whichever the machine can already run, in this order:

```
brew install gagarin-cloud/tap/gg
go install github.com/gagarin-cloud/gg@latest
```

If it has neither, take a binary for its platform from
https://github.com/gagarin-cloud/gg/releases — every release publishes
checksums, and you should verify them. **Do not pipe a script from a URL into a
shell**, do not download from a host you guessed, and do not carry on without
`gg`.

If `go install` succeeds but the shell still cannot find `gg`, its bin directory
(`go env GOPATH`/bin) is not on `PATH`. Say so and let the user fix their
profile; do not edit their shell configuration yourself.

`gg` shells out to **docker** for `build`, `push` and `registry copy` (the last
needs `buildx`). Nothing else needs it. A machine with no docker can still
deploy an image that is already in the registry, roll back, and read state.

Once installed, run `gg skill install` to refresh this skill from the binary, so
what you are reading matches the CLI you have. It installs for Claude Code by
default; `--agent cursor,codex` and friends, `--agent all`, or `-i` for a
checklist, cover the rest.

## Getting access

Signing up is open to any address — there is no list to be on, one email and one
button is the whole thing, and a new account starts with $5 on it and no card
asked for.

1. **Ask the user for their email address.** Do not guess it, and do not use one
   you found in the repository or in git history — a deploy that lands in a
   stranger's account is worse than no deploy.
2. `gg signup <email>` — it prints a code.
3. **Tell the user to press the button in the email**, and say the code, so they
   can check the email is the one you triggered.
4. `gg auth --claim <code>` — waits for the press, then stores credentials in
   `~/.config/gagarin/credentials.json` and logs `docker` in to the registry.

Signing up and authorising another machine are the same request, and the answer
is the same whether or not the address already has an account. The moment an
account is created it gets its balance and the address joins gagarin's customer
list; https://gagarin.cloud/privacy says what that list is for.

You never handle the credential yourself. Do not read that file, do not echo it,
and never ask the user for a token — if you find yourself wanting a secret to
make `gg` work, you are doing this wrong. The exception is CI, which gets its
own, below.

If `docker` was installed after gagarin, `gg registry login` does that half on
its own.

## Setting up CI

**Never run `gg signup` in CI, and never copy this machine's credential into
it.** A pipeline gets one of its own, minted from the one you already hold.

### 1. Mint the credential

```
gg creds create --name "github actions: acme/web"
```

The secret goes to **stdout alone on its line**; everything else goes to stderr.
So it pipes straight into a secret store and nothing has to touch disk:

```
gh secret set GAGARIN_TOKEN --body "$(gg creds create --name "github actions: acme/web" 2>/dev/null)"
```

Name it after where it will live. `gg creds` is a list somebody reads months
later deciding what is still wanted, and "token" tells them nothing then.

What you just minted is **deliberately weaker than what you hold**:

- It can deploy. **It cannot destroy** — a pipeline holding it cannot delete a
  project, a service or a database, and no flag turns that on.
- It **expires**: 90 days by default, 365 maximum, and there is no never.
- It **cannot mint another**, so a leaked one cannot issue its own replacements
  while you are busy revoking it.
- It is **shown once**. gagarin keeps a hash. Lose it and you revoke it and mint
  another; no command reads one back.

### 2. Give it to the pipeline

CI reads `GAGARIN_TOKEN` from the environment and needs nothing else: no
`gg auth`, no credentials file, no home directory, no interactive anything.

### 3. The workflow

The shape that matters: install gg, log docker in, publish on every commit,
release on the ones you mean.

```yaml
name: deploy
on:
  push:
    branches: [main]

jobs:
  deploy:
    runs-on: ubuntu-latest
    env:
      GAGARIN_TOKEN: ${{ secrets.GAGARIN_TOKEN }}
    steps:
      - uses: actions/checkout@v4

      - name: install gg
        run: go install github.com/gagarin-cloud/gg@latest

      - name: log docker in to the gagarin registry
        run: gg registry login

      - name: build and publish
        run: gg build acme/web:${{ github.sha }} --push

      - name: release
        run: gg deploy acme/web:8080 web:${{ github.sha }} --env-file .env.production

      - name: say what the cluster is doing
        run: gg status acme
```

Five things about that are load-bearing:

- **`gg registry login` is not optional.** `gg build --push`, `gg push` and
  `gg registry copy` all shell out to docker, and docker has no idea what a
  gagarin credential is until that runs. The failure without it is a `401` out
  of `docker push`, whose hint says exactly this.
- **Tag with the commit, not with the clock.** `gg build` invents a clock tag
  when you give it none, which is right for a person shipping and wrong for a
  pipeline: `${{ github.sha }}` is what makes a deploy reproducible and a
  rollback explicable.
- **`gg push` requires the tag** where `gg build` does not — a push moves
  something that already exists, and "whichever one I built last" is not a name.
- **The deploy step is separate on purpose.** Publishing on every commit and
  releasing on some of them is the whole reason build, push and deploy are three
  verbs. A workflow that only ever wants all three can run `gg ship` instead.
- **`gg status` at the end is a report, not a gate.** The deploy returned when
  the demand was recorded; the pod may still be pulling. Do not write a polling
  loop in CI — if you want a gate, gate on your own health check against the
  service's address.

Everything else works identically in any CI: the token in the environment, `gg`
on the PATH, docker available if you build. GitLab, Buildkite and Cloud Build
need no special handling.

### 4. Afterwards

```
gg creds              what has access, which are minted, when each expires
gg creds revoke 7     immediately, and needing no approval
```

Revoking needs no human because taking access away is always safe — a machine
you no longer trust should not wait on an inbox. Rotate a CI credential by
minting the new one, updating the secret, then revoking the old id.

If you are tempted to set `XDG_CONFIG_HOME` to a scratch directory and run the
signup flow again for a second credential: that used to be the only way and it
is not any more.

## Recipe: an application from nothing

The end-to-end, in the order that avoids every window where something is broken.

```
gg init shop                                  once, per application

gg resource add shop/db postgres --size m     the database, before the thing that needs it
gg ship shop/api:8080 --deps db               builds ., pushes, runs it, and it
                                              starts already holding DB_URL
gg ship shop/web:3000 --env API=http://api:8080
gg deps add shop/web api                      web may now call api
gg domain add shop/web                        the one thing the browser opens
gg status shop                                and only now tell the user a URL
```

Notes on each step, in the order they bite:

- **A Dockerfile per service.** Gagarin deploys images, so a service without one
  cannot be deployed. Writing it is a normal part of this job. Do not set a
  platform or architecture: gagarin reports what its own nodes run, and gg builds
  for that.
- **Run `gg ship` from the directory holding that service's Dockerfile**, or
  point `--context` at it — `gg ship shop/api:8080 --context ./api` from a
  monorepo root. `--file` names the Dockerfile when it is not called that, or
  lives outside the context directory.
- **The number after the colon is the port the container listens on.** It
  defaults to 8080. Say it anyway when you know it: a service answering on the
  wrong port is a hang, not an error.
- **`--deps db` on the first ship** means the pod never starts into a window
  where it cannot reach the database and does not hold its password. On later
  ships it is optional and harmless.
- **Private services talk over `http://<service-name>:<port>`** — the service
  name, no project prefix, no scheme guessing.
- **`gg domain add` for the service the user's browser opens, and nothing else.**
  Do not expose a database, a worker or an internal API "so it can be tested".
  Ask the user before putting anything on the internet they did not ask to.
- **`gg status` before you promise anything.** `gg ship` says nothing about where
  a service answers, deliberately — an address exists as a string long before
  anything is listening on it.

### When to use the three steps instead

```
gg build  shop/web:v3 --context ./web    make an image, run nothing
gg build  shop/web:v3 --push             ...and upload it in the same call
gg push   shop/web:v3                    publish it, release nothing
gg deploy shop/web:8080 web:v3           release one that already exists
```

That is CI, and it is also the only way to run an image you did not just build —
one copied in with `gg registry copy`, or a tag somebody else pushed.

## Services

A service is a container image that runs, with a port, a size, an environment,
optionally a volume, whatever it is allowed to reach, and whatever addresses it
answers on. Only the first two of those are set by a deploy. An image that runs
to completion instead of serving — a migration, a backfill — is a **job**, not a
service: see "Jobs" below, and do not deploy one as a service, because a
service that exits is restarted forever and reads as failing.

### The environment

```
gg deploy shop/web:8080 web:v3 --env-file .env
gg deploy shop/web:8080 web:v3 --env-file .env --env DEBUG=false
gg ship   shop/web:8080 --env-file .env
```

- `--env-file` is **not** picked up automatically. If the user has a `.env` and
  wants it used, pass the flag; never assume a file should be read.
- Precedence: `--env` beats every file, later `--env-file` beats earlier ones,
  and **a resource's injected variable beats all of it** (see "Resources").
- There is **no interpolation**. `B=${A}` sets `B` to the literal `${A}`.
- Env is **replaced on every deploy, not merged.** To remove a variable, deploy
  without it. To keep one, keep passing it.
- The file supplies **values only**. It cannot name services, set ports or say
  what is public. If you find yourself wanting to put deployment structure in a
  file, stop — gagarin holds that itself.

Environment is the one thing a deploy still replaces wholesale, because it is
part of what a revision ran with and is what a rollback puts back. Everything
else that could be lost by forgetting to restate it has been moved out of a
deploy for exactly that reason.

### Size

Every service and every resource runs at a size, and `s` is what you get if you
say nothing at creation.

| | reserved | ceiling | price |
|---|---|---|---|
| `s` | 100m CPU / 256 MiB | 0.5 vCPU / 1 GB, shared | $10/month |
| `m` | 1 vCPU / 2 GB | same — dedicated | $30/month |
| `l` | 2 vCPU / 4 GB | same — dedicated | $60/month |

```
gg deploy shop/web:8080 web:v3 --size m
gg ship shop/web:8080 --size m
gg resource add shop/db postgres --size m
```

- **`s` is shared; `m` and `l` are dedicated.** An `s` service may burst to its
  ceiling but is only guaranteed the reservation, which is why it costs a third
  of `m`. An `m` or `l` reserves exactly what it is promised and is the last
  thing evicted when a node runs short.
- **Omitting `--size` keeps the size the service already has.** It does not reset
  to `s`. Changing a size is therefore always deliberate.
- **Changing a size restarts the service** — a new pod with new limits. Expect a
  few seconds of downtime, and do not do it while something depends on it.
- **Which to pick.** Start at `s`: it runs a small API, a worker, a static site,
  a database you develop against. Move to `m` when something is OOM-killed —
  `gg status` says so and names the size to move to — or when you already know
  the workload: server-side-rendered Next.js, a JVM, a large in-process cache, a
  postgres a real application depends on. Do not reach for `l` speculatively.
- `gg status` shows the size every service is running at, and the day's accrued
  cost at the bottom of the table.

### Volumes

```
gg ship shop/db:5432 --volume /var/lib/postgresql/data --volume-size 20
```

A directory that survives restarts. **Set once, at the deploy that creates the
service**; a later deploy cannot move it or resize it, and asking is refused with
`volume_immutable`. The default ceiling is 10 GB. A rollback across a change of
volume is refused for the same reason.

Use this for anything stateful that has no resource type — see "Anything we do
not have a type for".

## Jobs: what runs to completion

```
gg build shop/migrate:v3 --context ./migrations
gg run   shop/migrate migrate:v3 --deps db --env-file .env
gg run   shop/migrate migrate:v3 --detach        submit and return
```

A job is a service that ends. It has an image, an environment, a size and a
place on the graph, and **no port, no address and no volume**: it listens on
nothing, nothing can declare that it needs one, and it keeps nothing between
runs. Durable data belongs in a resource the job reaches with `--deps`.

- **`gg run` is the one command that waits.** It submits the run, follows it,
  prints what the run wrote, and **exits with the script's own exit code**. Read
  the code: zero means the script finished; anything else is the script's
  failure, and the last line names it. `--detach` returns at once instead, and
  `gg status` then reports how the run ended.
- **Every `gg run` is one run**, recorded as a revision exactly like a deploy.
  Running twice runs twice. `gg history` lists the runs; `gg rollback` runs an
  earlier revision again.
- **A run that fails is not retried**, and its exit code is reported once. Fix
  the script and run again; the platform will not re-run a half-applied
  migration on its own.
- **A run is stopped after sixty minutes.** `gg status` says so when that is
  why it ended. Something that needs longer is a service.
- **Its image is any image in the project's space** — build one with
  `gg build`, or run the service's own image with a different entrypoint baked
  into a second Dockerfile. `gg run` does not build.
- **A job is billed for the time it runs**, at its size, not for existing.
- **Kinds do not change.** A name that is a job stays a job (`not_a_service`),
  and a name that is a service cannot be run as a job (`not_a_job`). Give the
  job its own name.

Use a job for anything the user describes as "run this once" or "run this
before deploying": migrations, seeds, imports, one-off reports. The shape for a
deploy that needs a migration first is two commands, in this order:

```
gg run    shop/migrate migrate:v3 --deps db     # exits non-zero if it failed: stop here
gg deploy shop/api:8080 api:v3
```

## Dependencies: what may reach what

Every service in a project is default-denied. A private service is reachable
only from services that have declared they need it, and **an undeclared call is
dropped rather than refused** — it does not fail fast, it hangs until the client
gives up, which for a database driver can be thirty seconds or forever.

**A hang between two of your own services is a missing `gg deps` far more often
than it is a bug in the application.**

```
gg deps ls  shop/api            what it reaches today
gg deps add shop/api db cache   and these as well
gg deps rm  shop/api cache      and no longer that one
```

- **The direction matters.** The declaration goes on the *caller*. If `api`
  queries `db`, it is `api` that needs `db`, never the other way round. Backwards
  is refused, not quietly accepted.
- **Reaching a resource also hands over its credentials.** `gg deps add shop/api
  db` gives `api` `DB_URL`, `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD` and
  `DB_DATABASE`, and the command prints which ones arrived. Connecting is this
  one call; it used to be two, and that is no longer necessary.
- **`--deps` on a deploy or a ship only ever adds.** It exists so a service that
  cannot start without a database can be declared and deployed together.
  Forgetting it on the next deploy changes nothing. Withdrawing is `gg deps rm`,
  there and nowhere else. (If you are working from memory and reach for
  `--needs`: it is gone. It replaced the whole set on every deploy, so a redeploy
  that forgot it silently withdrew an edge — and because an undeclared call
  hangs, the service did not break, it went quiet.)
- **Public URLs are not covered by this.** One service calling another's public
  `https://` address arrives the way a stranger's browser does and is allowed.
  The guarantee is about private, in-project addresses.
- **Withdrawing applies to new connections, not open ones.** `gg deps rm` closes
  the path for anything that connects afterwards, but a client holding a
  keep-alive connection — every HTTP client, every driver pool — keeps using it
  until it reconnects. That is how Kubernetes network policy works. If you need
  the old path gone now, redeploy the caller: a new pod has no old connections.
- `gg destroy` on something another service still needs is refused, and names
  what depends on it. `gg deps rm` that first.

`gg status` ends every service's row with a REACHES column, so checking the
graph costs nothing — do it *before* you go debugging a hang.

### Reaching the outside world

Outbound is unrestricted, with one exception you need before you build on it:
**the mail ports — 25, 465 and 587 — are blocked.** A connection to any of them
times out, from every service, and it cannot be opened per-account.

So **do not build anything that speaks SMTP.** If the user wants their
application to send email, reach for a provider's HTTPS API — Resend, Postmark,
SendGrid, SES, any of them. That is a normal way to send mail, not a workaround,
and it is what gagarin itself does. Prefer the provider's official SDK to
hand-rolled HTTP.

The reason, if the user asks: a service that can open those ports can relay spam
if it is ever compromised, and a hosting account that relays spam gets suspended
— which would take every other project on the platform down with it.

If you find yourself debugging a hanging SMTP connection, stop. It is not the
user's code and it is not DNS. It is this.

## Resources: what a project *has*

A resource is provisioned rather than deployed. There is no image to choose, no
version to pass and no Dockerfile.

```
gg resource add shop/db postgres --size m --storage 20
gg resource add shop/vectors qdrant
gg resource add shop/cache valkey
gg resource add shop/openai external --env-file .env.openai
```

| type | what it is | storage | backups |
|---|---|---|---|
| `postgres` | PostgreSQL 17 | on a volume; survives restarts | nightly, kept 14 days |
| `qdrant` | vector database, for retrieval | on a volume; survives restarts | none yet |
| `valkey` | in-memory store, redis protocol | **none — a restart loses everything** | none, by design |
| `external` | a third-party API, run by nobody here | **none — it runs nothing at all** | nothing to back up |

The types are named for what actually runs, not for the products they stand in
for — an honesty you can pass on when somebody asks.

- **`qdrant` is Qdrant**, Apache-licensed, and it is what an application does
  retrieval against: embeddings in, nearest neighbours out. Three things about
  it are unlike every other type, and all three will bite an agent that assumes
  otherwise.
  - **The credential is not in the URL.** `<NAME>_URL` is a bare
    `http://vectors:6333`; the key arrives separately as `<NAME>_API_KEY` and
    travels as an `api-key` header. Every client takes it as its own argument —
    `QdrantClient(url=os.environ["VECTORS_URL"], api_key=os.environ["VECTORS_API_KEY"])`.
  - **It answers on two ports.** `<NAME>_PORT` is 6333 (HTTP/REST) and
    `<NAME>_GRPC_PORT` is 6334. The Python, JS, Java and .NET clients want the
    URL; the official **Go and Rust clients speak only gRPC** and want the host
    and 6334. Both are published so neither has to be guessed.
  - **There is no MongoDB here, and no document store either.** If a user wants
    documents rather than vectors, gagarin has no type for it — run one as an
    ordinary service with a volume.

  Gagarin ran a `ferretdb` type from 2026-09-04 to 2026-09-06 and it is **gone**:
  it promised MongoDB compatibility and did not keep the promise past the simple
  cases. There is no alias and no migration path. If a user names it, say it was
  removed and offer the service-with-a-volume route.
- **`valkey` is a cache, not a database.** `--storage` is refused (`no_storage`)
  rather than quietly ignored, and everything in it is gone when the pod restarts
  — which happens on a node drain, not only when somebody asks. Do not put a
  session store there that a user would notice losing, and do not tell anyone
  their data is safe in it. Valkey is the BSD-licensed fork of Redis; `redis://`
  URLs, `redis-cli` and every client library work unchanged.
- **Storage can grow, never shrink.** Restate the resource with a bigger
  `--storage` to raise the ceiling — `gg resource add shop/db postgres --storage
  40` on an existing `db` grows it and changes nothing else. A smaller number is
  refused with `volume_immutable`.
- **One instance, one volume, no failover.** A size change or a restart is a
  brief outage. There is no point-in-time recovery. Say that plainly if a user
  asks whether their data is safe, rather than implying an SLA nobody is on the
  hook for.
- **A resource cannot be deployed over.** `gg deploy shop/db` against one is
  refused with `not_a_service`, which is telling you the name is already a
  database. Nor can a resource declare dependencies: it is reached, it does not
  reach.
- **A managed resource cannot be rolled back**, because gagarin mints its
  credentials and there is no earlier value of the user's to go back to. An
  **external can** — its values are theirs — and `gg rollback P/NAME` is how a
  config change is undone. See the external section below.

### Connecting one: the single call

```
gg deps add shop/api db
```

That declares the dependency **and** puts the database's connection variables in
`api`'s environment. It prints what arrived:

```
api reaches db, and nothing else.

api now holds DB_DATABASE, DB_HOST, DB_PASSWORD, DB_PORT, DB_URL, DB_USER
  in its environment, from the resources it reaches. Values: gg resource secrets.
```

Or declare it on the deploy, so the service never starts without them:
`gg deploy shop/api:8080 api:v3 --deps db`.

**The variables are named after the resource, not the protocol** — the resource's
own name, upper-cased with dashes as underscores, plus a suffix:

| suffix | postgres | valkey | qdrant |
|---|---|---|---|
| `<NAME>_URL` | `postgres://…?sslmode=disable` | `redis://:pass@…` | `http://vectors:6333` |
| `<NAME>_HOST` | ✅ | ✅ | ✅ |
| `<NAME>_PORT` | 5432 | 6379 | 6333 |
| `<NAME>_GRPC_PORT` | — | — | 6334 |
| `<NAME>_USER` | ✅ | — | — |
| `<NAME>_PASSWORD` | ✅ | ✅ | — |
| `<NAME>_API_KEY` | — | — | ✅ |
| `<NAME>_DATABASE` | ✅ | — | — |

A postgres called `db` publishes `DB_URL`; one called `orders-db` publishes
`ORDERS_DB_URL`; a valkey called `cache` publishes `CACHE_URL` and no
`CACHE_USER`, because valkey has no users. A type omits the suffixes it has no
answer for rather than publishing them empty — so an absent `VECTORS_PASSWORD`
is not a bug, it is a qdrant having no password. Two consequences, and both are
the point: a service can reach two databases without a collision, and there is
one vocabulary of suffixes rather than a different set per type.

**These are not the names your framework expects, and that is deliberate.** There
is no `DATABASE_URL`, no `PGHOST`, no `REDIS_URL`, no `QDRANT_URL` unless the
resource happens to be called `qdrant`. The first thing to try is to have the
application read `DB_URL` — one line in the app, and nothing about it can go
stale.

If you genuinely cannot change the application, read the value out and pass it
under the name it wants:

```
gg deploy shop/api:8080 api:v3 --deps db \
  --env DATABASE_URL="$(gg resource secrets shop/db --format json | jq -r .env.DB_URL)"
```

Note what that is: **a copy**, and the only stale thing in the model. `$DB_URL`
alone would not work — the injected variables exist inside the pod, not in the
shell running `gg`. Say so rather than leaving a second copy of a password in a
deploy nobody remembers making.

Four rules that follow, and each one fails quietly if you get it wrong:

- **Injected variables outrank your own.** Set `DB_URL` on a deploy while the
  service also reaches a resource called `db`, and the resource wins. It is the
  one place something beats what a deploy said, and the reason is that only the
  resource knows the real password.
- **Nothing is copied into the stored environment.** The platform unions these in
  every time it renders the pod, from the graph as it stands then. They cannot go
  stale, no redeploy can forget them, and **a rollback restores the image and the
  environment you deployed with, never an old password.** `gg deps rm` takes them
  away just as surely.
- **Holding the credentials is not enough.** The network policy is a separate
  boundary from the environment. You cannot get the credentials without the
  declaration that opens the route, so this is no longer a trap — but do not
  conclude from a working password that the route is open for some *other*
  service you did not declare.
- **`gg resource secrets` is for reading the values from outside the graph** —
  checking what an application is being handed, or wiring up a client that runs
  outside gagarin. The values name the in-project host, which this machine
  cannot reach: to actually connect from here, `gg connect` opens a tunnel and
  prints them rewritten for it. You do not need either to connect a service in
  the same project. Treat the output as a live credential: do not echo it into
  a chat, a commit, or a summary.
- **`--names` when the question is "what does it publish"**, which it usually
  is: what a bundle holds, or what a key is called before you change it. It
  asks a different endpoint, so the values are not fetched rather than merely
  not printed — nothing secret passes through you. `gg status` cannot answer
  this: it prints `DB_*`, the prefix rule rather than the contents.

```
gg resource secrets shop/db --names         # just the names — reach for this first
gg resource secrets shop/db                 # KEY=VALUE lines, for --env-file
gg resource secrets shop/db --format json   # for jq
```

### Meddling from this machine: `gg connect`

```
gg connect shop/db
```

A resource is not on the internet, and stays off it. When a human needs psql
against the real database — a migration to babysit, a row to look at — this
opens a tunnel from this machine and prints the same variables a service would
hold, rewritten to point at 127.0.0.1:

```
db is a postgres. The tunnel is up:

  DB_HOST=127.0.0.1
  DB_URL=postgres://app:…@127.0.0.1:5432/app?sslmode=disable
  ...
```

- **It lasts exactly as long as the command.** Ctrl-C closes the tunnel and
  every connection through it. Nothing is exposed, and there is nothing to
  undo afterwards.
- **It is for the machine gg runs on, never for services.** A service reaches
  a resource by declaring it — `gg deps add shop/api db` — which needs no
  tunnel and survives any command exiting. Do not leave `gg connect` running
  as plumbing under an application.
- **Every port the resource answers on gets a local end** — a qdrant tunnels
  6333 and 6334 at once. Each prefers its own number and quietly takes a free
  one when that is busy; `--port` pins the primary, and is refused rather than
  moved when the port is taken.
- **An external is refused** (`not_tunnelable`): nothing runs, so there is
  nothing to tunnel to. Its values are `gg resource secrets`.
- **The resource must be running.** A stopped one refuses with
  `tunnel_unavailable`, and `gg status` says what it is waiting on.
- The printed values are live credentials on a local port, and the same rule
  as secrets applies: do not echo them into a chat, a commit, or a summary.

### `external`: a third-party key as a node on the graph

An OpenAI account, a Stripe key, a bucket somewhere else. Not something gagarin
runs — something your services need a credential for.

```
gg resource add shop/openai external --env-file .env.openai
gg deps add shop/bot openai
```

An `.env.openai` holding `API_KEY` and `BASE_URL` makes `bot` hold
`OPENAI_API_KEY` and `OPENAI_BASE_URL`. Same naming rule as every other type, so
**write the keys without the prefix** — `API_KEY`, not `OPENAI_API_KEY`, which is
refused rather than doubled.

**Why bother, instead of `--env OPENAI_API_KEY=…` on the deploy?** Because a key
on a deploy is a copy. Three services using one key means three copies, rotating
it means three deploys, and a deploy you forget leaves a service authenticating
with a revoked key. As a resource it is one row and rotating is one command.

- **Use `--env-file`, not `--env`.** A key on the command line goes into the shell
  history and into the transcript of every agent that ran the command. If the
  user hands you a key in chat, write it to a file first, use the file, and tell
  them that file holds a live credential.
- **Restating an external is refused** once it exists (`already_exists`). That
  refusal is deliberate: restating from an old `.env` would roll the key
  *backwards*, and the failure would be an authentication error nobody would
  connect to the command they ran. Changing values is `gg resource rotate`, and
  changing *one* of them is `gg resource rotate --set K=V`, which leaves the
  rest alone.
- **It runs nothing.** No container, no port, no size, no storage, no backups,
  nothing to become ready. `gg status` shows it as `◆` and leaves every
  container column a dash; what it publishes is `gg resource secrets P/NAME
  --names`.
- **Declaring an external does NOT restrict egress.** This is the one thing not to
  get wrong when you explain it. Everything in a project can already reach the
  whole internet — the mail ports are the only exception. What the declaration
  grants is the credentials and a line on the graph saying who uses them. It is
  **not** a firewall: an undeclared service can still call `api.openai.com`, it
  just has no key. `gg status` marks these edges `◆` for exactly this reason.
- **What it is genuinely worth:** the project's dependencies become legible, and
  destroying an external is refused while anything still declares it, so a key
  cannot be dropped out from under a running service.

### An external is also where shared config belongs

Not only third-party credentials. **Anything several services read, or that
changes without a deploy, belongs in an external** — feature flags, a log level,
a region, an API base URL, a tuning constant.

```
gg resource add shop/config external --env-file .env.shared
gg deps add shop/web config
gg deps add shop/worker config          # both hold CONFIG_LOG_LEVEL

gg resource rotate shop/config --set LOG_LEVEL=debug   # both roll, with the new value
gg rollback shop/config                                # both roll back
```

**Why this beats `--env` on each deploy:** the same argument as for a key. An
env on a deploy is a copy, so two services sharing a setting is two copies that
can disagree, and changing it is two deploys with one you can forget. Resolved
from the graph it is one row, one command, and every holder restarts.

**The editing model is the sharper difference, and it is the one that decides
this for you.** A service's environment can be changed only by `gg deploy`, and
a deploy **replaces it wholesale** — every variable not restated is gone. That
is deliberate, not an oversight: the revision records exactly what the service
ran with, which is what makes a rollback mean something. The cost is that
changing one variable requires having all of them.

|  | service env | external |
|---|---|---|
| changed by | `gg deploy`, and only a deploy | `gg resource rotate` |
| granularity | wholesale — anything omitted is **lost** | one key at a time |
| needs the other values in hand | **yes** | no |
| undo | `gg rollback P/SVC`, with the deploy | `gg rollback P/config` |

**For you this is a hard edge, not an inconvenience.** Working without the
project's `.env` file — from the console's Agent tab, or any session handed a
task rather than a repository — you cannot safely change one variable on a
service. The only route is to read the whole environment back out of
`gg history` and restate it, which drops anything you misread and pulls **every**
value the service holds, secrets included, through your transcript.

`gg resource rotate P/config --set KEY=value` has neither problem: it touches the
one key, needs nothing else in hand, and reads no other value. **So when a user
asks you to change a setting and you do not have their env file, the answer is
an external — and if the setting is currently in a deploy env, say so and offer
to move it.**

And to find out what a bundle holds before you change one key of it:

```
gg resource secrets shop/config --names      # names only; no values fetched
```

Use `--names` by default. Without it the command prints live credentials, and
in your case that means into a transcript. `gg status` will not answer this
either — it lists the resource and nothing about its contents.

The full lifecycle is there, which is what makes it safe to recommend:

| | |
|---|---|
| set it | `gg resource add P/config external --env-file .env` |
| change one value | `gg resource rotate P/config --set LOG_LEVEL=debug` |
| remove one | `gg resource rotate P/config --unset REGION` |
| see what it was | `gg history P/config` |
| put it back | `gg rollback P/config [--to N]` |
| see what keys it has | `gg resource secrets P/config --names` — no values fetched |
| read the values | `gg resource secrets P/config` — live credentials; prefer `--names` |
| who uses it | `gg status P` — and destroying it is refused while anyone does |

**Where the line is.** Config *owned by one service* stays in its `gg deploy
--env`, because that is the half a service rollback restores. Injected values
are re-derived from the resources as they stand now — deliberately, so nobody is
ever rolled back onto a rotated password — so `gg rollback shop/web` will not
undo a config change. Undo it at the resource.

Three properties to state when asked, because they are constraints and not
oversights:

- **The prefix is not optional.** A resource named `config` publishes
  `CONFIG_LOG_LEVEL`, never a bare `LOG_LEVEL`. Name the resource so the prefix
  reads the way the application wants it.
- **A dependent cannot override a value.** Injected beats the service's own env
  of the same name. Two services needing different values is two resources.
- **Every dependent gets every key** in the bundle. Split by audience, not by
  topic: one resource per set of services that should hold the same things.

### Rotating credentials

```
gg resource rotate shop/db                                a database
gg resource rotate shop/openai --set API_KEY=sk-new       one of an external's values
gg resource rotate shop/openai --env-file .env.new        all of them
```

Everything holding the old credential is restarted with the new one, and the
command names what it rolled. Nothing but variable *names* is printed;
`gg resource secrets` reads the values.

**Who supplies the new value is the only difference between the types.** For
`postgres`, `qdrant` and `valkey`, gagarin mints one and `--env` is refused — a
password you chose is one the running server has never heard of. For an
`external` the values are yours, so one of `--set`, `--unset`, `--env-file` or
`--env` is required.

**An external usually holds more than one value, and the two ways of changing
them mean different things. Reach for `--set`.**

| | what it means |
|---|---|
| `--set K=V`, `--unset K` | change these, leave every other key exactly as it is |
| `--env`, `--env-file` | the bundle is now precisely this; anything not in it **stops being published** |

`--env API_KEY=sk-new` on an external that also holds `BASE_URL` takes `BASE_URL`
away from every dependent. That is the correct meaning of that request — it is
the one to use after a provider migration where every value is new — and the
wrong one for the far commoner job of replacing a single key. Reading the bundle
back and restating it is not the answer either: that is the stale-file path, and
it is how a key gets rolled backwards.

The command names what stopped being published, so a loss is visible at the
moment it happens rather than from a dependent that can no longer authenticate.
The two flags cannot be combined — they say the same thing in incompatible
words.

`--unset` names a key **without** the resource's prefix, the way it was set:
`--unset BASE_URL` on `shop/openai`, not `OPENAI_BASE_URL`. A key the resource
does not publish is refused rather than quietly doing nothing, because a no-op
reported as a success is how somebody believes a secret is gone when it is not.

| type | what happens | cost |
|---|---|---|
| `postgres` | the running server is told immediately | none |
| `qdrant` | the pod is replaced; the key is read at startup | a few seconds away; **the data survives** |
| `valkey` | the pod is replaced; the password is read at startup | **the cache is emptied** |
| `external` | nothing of ours runs, so nothing of ours restarts | none |

**If it fails, nothing changed.** The old credential is still in use and the
command is safe to run again — say that, rather than leaving the user wondering
what a half-failed rotation left behind. There is no half.

**Rotate after a leak, and say so plainly.** If a key has been in a chat log, a
commit or a screenshot, this is the fix and it is one command. For an external,
note that gagarin forgets the old value but cannot *revoke* it — that is a thing
to do in the third party's own console, and the user has to do it.

### Backup and recovery

Postgres is dumped nightly and kept fourteen days. Nothing else has backups.

```
gg resource backups shop/db     what is stored, newest last
gg resource backup  shop/db     take one now — do this before a risky migration
```

`gg resource backup` reads the database and writes an object; it is safe at any
time and the nightly schedule keeps running regardless.

**A restore creates a NEW resource — it never overwrites an existing one.** That
is the platform's rule, not a convention: the engine refuses to restore into a
database that already holds data, which is exactly why a restore needs no human
approval and can be reached for at three in the morning.

```
gg resource restore shop/db2 --source db   new resource, filled from db's newest dump
gg deps add shop/api db2                   hands api DB2_URL and the rest
gg deps rm  shop/api db                    and stop it reading the old one
gg destroy  shop/db                        once everything reads from db2 — the only
                                           step that destroys data, and the only one
                                           that asks a human
```

- **The variables change with the name.** `db2` publishes `DB2_URL`, not
  `DB_URL`, so an application reading `DB_URL` finds nothing after the `deps rm`.
  Either name the new resource what the old one was called — possible only once
  the old one is destroyed — or expect to touch the application. This is the one
  place the per-resource naming costs something, and it is worth knowing before
  three in the morning.
- The old resource **may already be destroyed** when you restore: its backups
  outlive it by fourteen days and `--source db` still finds them.
- `--backup <key>` from `gg resource backups` restores an exact point instead of
  the newest.

### Anything we do not have a type for

Four types will never cover everything. Run it as an ordinary service with a
volume:

```
gg registry copy shop/clickhouse clickhouse/clickhouse-server
gg deploy shop/analytics:8123 clickhouse --volume /var/lib/clickhouse --volume-size 50
gg deps add shop/api analytics
```

Everything else behaves the same way: a private address by name, a volume that
survives restarts, and the same refusal to delete it while something still needs
it. The only difference is who decides — for a resource the platform picks the
image, the version and the port and carries them; here you pick them, and
upgrades, tuning and consequences are yours.

**If a type exists, use the resource. If it does not, this is the normal path,
not a workaround.** Gagarin has no type for ClickHouse, Cassandra, DuckDB or a
document store, and that is not an error.

Never write a one-line Dockerfile that only says `FROM` in order to get an image
in. `gg registry copy` is the same thing, done properly, and it copies every
architecture the source published rather than whatever your laptop cached.

## Addresses

A service is private until it is given an address, and one command gives it
either kind:

```
gg domain add shop/web                     an address gagarin generates
gg domain add shop/web shop.example.com    a name the user owns, as well
gg domain ls  shop                         every address, and who each is waiting on
```

The generated one is idempotent and instant: gagarin holds the wildcard record
and the wildcard certificate, so there is nothing to coordinate and nobody to
wait for.

**A name the user owns is two steps, and only the first is gagarin's.** The
command makes the service answer for the name; making the name *resolve* here is
a DNS record the user creates at their registrar, and gagarin cannot do it for
them. The command prints the exact record — pass it on verbatim rather than
paraphrasing it.

**Nothing is served over HTTPS on that name until the record exists.** Let's
Encrypt proves control by fetching the domain over the internet, so a certificate
cannot be ordered before DNS points here. That is the normal first state, not a
fault: do not retry the command hoping it resolves, because the missing piece is
on their side. `gg status` says which of the two of you it is waiting on:

| reading | who acts |
|---|---|
| waiting for DNS | **them** — the record does not exist yet |
| DNS points elsewhere | **them** — it resolves, but not to gagarin |
| issuing certificate | gagarin — nothing for them to do |
| ok | nobody |

The generated address only ever reads `ok` or `issuing certificate`. It is never
waiting on the user, because there is nothing for them to do about it.

- **A deploy never changes an address**, in either direction. Forgetting a flag
  would otherwise take a live site down while the owner's DNS still looked right.
- **Both addresses answer.** A service with a custom domain keeps its generated
  one, so existing links do not break and there is something to test against
  while DNS propagates.
- **A custom domain brings the generated one with it.** Claiming a name for a
  private service makes it public in the same call. Say out loud that you are
  putting it on the internet.
- **One domain, one service, across all of gagarin.** A name somebody else holds
  is refused, and the refusal does not say who holds it.
- **An apex domain gets an A record, not a CNAME** — DNS does not permit a CNAME
  at an apex. The command prints the right one; do not "correct" it.

### Taking an address away

```
gg domain rm shop/web shop.example.com    release a name they own
gg domain rm shop/web                     make the service private again
```

**Both need a human's approval, every time** — the same emailed click `gg
destroy` needs. This is destructive in the way deleting something is: what breaks
is invisible from the terminal and obvious to whoever was using the address. Do
not run either unless the user asked for it in those words.

Releasing the generated address while a custom name is still declared is refused
(`domain_attached`) — that would leave their DNS pointing at a host gagarin no
longer serves. Release the custom name first.

## Reading state

`gg status <project>` reports desired state and actual cluster state side by
side. Trust it over your own memory of what you deployed: it reads the cluster,
not just the database.

```
project shop  (id 7f3a9c2e)

     SERVICE  KIND      SIZE  READY  PORT  REACHES      IMAGE
  ●  web      service   s     1/1    8080  api          web:1757030400
  ●  └ https://web-7f3a9c2e.gagarin.cloud
  ●  api      service   m     1/1    4000  db, openai◆  api:1757030112
  ●  db       postgres  m     1/1    5432  —            postgres:17
  ◆  openai   external  —     —      —     —            —

  ● running   ◆ external (runs nothing; declaring it grants its variables, not egress)
  $0.412 today so far
```

What to read, in the order it matters:

1. **Lines beginning `!`, when there are any, come before the table because they
   change what the table means.** There are two:
   - **"this project is suspended and nothing is running: …"** — every service
     shows `◌` and `0/0`, and writes are refused with `project_suspended`. If the
     reason is credit, the user fixes it at https://my.gagarin.cloud/billing and
     services restart by themselves within a few minutes. Anything else is a
     decision gagarin made, and only support can lift it — do not tell the user
     to add credit, because it will not help. **Nothing has been deleted**: the
     data, the addresses and the certificates are all still there.
   - **"the reconciler last ran … ago"** — gagarin reporting on itself. The rows
     are still an accurate reading of the cluster, but nothing is closing the gap
     between them and what was asked for. Tell the user; there is nothing an
     agent can do from here.
2. **The state mark.**
   - `●` **running** — the cluster is running what was asked for, and something
     answers on the declared port.
   - `◐` **starting** — in flight: pulling, booting, waiting on an ingress. The
     cluster has not given up and neither should you.
   - `○` **failing** — either nothing is in the cluster at all, or Kubernetes has
     stopped calling this a rollout in progress. Waiting will not fix it.
   - `◌` **stopped** — the project is suspended. See the `!` line.
   - `◆` **external** — runs nothing, so it has no state to be in.
   - `✓` **done** — a job whose latest run finished with exit code 0. A job
     whose run failed is `○`, and the line under the table gives its exit code.
3. **`◐` and `○` print the cluster's own explanation below the table.** Read it
   before changing anything.
4. **READY counts pods of the revision you asked for.** A redeploy that will not
   start reads `0/1` even while the previous version is still serving traffic.
   That is the honest number: the service is answering, but not with what you
   shipped. For a job the cell is the latest run's phase instead — `done`,
   `failed`, `running`, `pending` — its PORT is a dash, and the line under the
   row says which run, when, how long, the exit code, and for a failed run the
   cluster's reason: `○  └ run 4 failed 2 minutes ago after 3s, exit 2: the
   container exited with code 2`.
5. **Addresses hang under their service**, marked `●` when there is nothing left
   to do and `○` with who is holding it up when there is. An address that is fine
   says nothing more than its own URL.
6. **KIND and VOLUME columns appear only when the project has something to put in
   them**; SIZE is always there.
7. **The last line is the day's accrued cost**, in thousandths of a dollar.

Other readers:

```
gg projects                 every project you can reach, its id, and your role
gg logs shop/web            recent logs; needs a running pod (logs_unavailable if not).
                            For a job: what its latest run wrote
gg history shop/web         every deploy, newest first, the live one marked →
gg deps ls shop/web         what it may reach
gg domain ls shop           every address and its state
gg creds                    every credential, the one you are using marked *
```

When somebody wants to understand how their services fit together, point them at
the project's page on https://my.gagarin.cloud — its right half is a live
dependency graph. That is for the human; read the plain output yourself.

Describe services at the altitude gagarin does: "web is public on port 8080 at
<url>; worker is private." Give every address a service answers on, not just one.
Do not introduce Kubernetes vocabulary — namespaces, ingresses, pods and service
accounts are not part of this model, and mentioning them is a regression.

## Undoing a deploy

```
gg history  shop/web          revision numbers, images, when, and who
gg rollback shop/web          put the previous deploy back
gg rollback shop/web --to 3   a particular revision
gg rollback shop/config       put an external's previous values back
```

- **A rollback is a deploy.** It goes through the same write gate and is recorded
  as a *new* revision naming the one it restored. Nothing leaves the history, so
  changing your mind is another rollback rather than a lost record.
- **It restores the image and the environment**, exactly as that revision had
  them. A variable added since is gone afterwards.
- **It does not restore the dependency graph, the domain or the volume.** Those
  are standing declarations about the shape of the project rather than parts of
  the artifact, and putting yesterday's image back says nothing about them.
- **It never restores an old password.** Resource variables come from the graph
  at pod-start, not from the revision.
- **It is refused across a change of volume** (`volume_immutable`). Deploy the
  configuration you want instead.

Prefer a rollback to a corrective deploy when something you just shipped is
broken and you do not yet know why: one call, a state that provably ran, and the
evidence left intact for afterwards.

## Sharing a project

A project has exactly one **owner** — the account that pays for it — plus any
number of editors and viewers.

```
gg members shop                     who can reach it, and as what
gg share shop teammate@example.com  add an editor (the default)
gg share shop them@example.com --role viewer
gg unshare shop them@example.com
```

An **editor** operates the project — deploy, delete individual services, manage
the roster — without paying for it. A **viewer** reads status, logs and the
member list, and nothing else. **Destroying a project is the owner's alone**:
that takes its data and its URL with it, and only the account paying can decide.
Ownership is not on this list because it is the bill, not a role: it is not
granted here, it is offered and accepted — see below.

- `gg share` with no `--role` grants **editor**, which can deploy over whatever
  is running. If the user asked for "read access" or "let them look at the logs",
  pass `--role viewer`.
- Sharing with somebody who has never used gagarin is allowed — the access waits
  for them and they get it when they sign up with that address. **Nothing is
  emailed to them**, so tell the user to let them know.
- **Ask before sharing.** Access to a project is the user's to give, not yours to
  infer from a name in the conversation.

## Handing a project over

Sharing gives access away. `gg transfer` gives away the **bill** — who pays for
the project from that moment on.

```
gg share shop them@example.com       first: they must already be a member
gg transfer shop them@example.com    offer it; nothing changes yet
gg transfer shop --withdraw          take the offer back
gg members shop                      shows an offer that is standing
```

It is an offer, never an assignment, and both halves need a human:

1. The **owner** runs `gg transfer` and gets `approval_required` — they click a
   button in their own inbox, and you run the same command again. This is the
   same human gate `gg destroy` uses, for the same reason: a handover cannot be
   undone by the person who started it.
2. The **recipient** gets an email and presses a button. Until they do, nothing
   has changed and `gg members` still shows the old owner. **Tell the user this**
   — a transfer that is "done" from the CLI is a transfer that has not happened.

What happens when they accept: the project keeps its id, services, addresses,
data and history — **nothing restarts**. Usage is billed to the new owner from
that moment, and to the old owner up to it. The **previous owner becomes an
editor**, so they keep operating it and stop paying for it; the new owner can
`gg unshare` them like anyone else.

- **Only the owner can offer**, and only to somebody already on the roster. Share
  first.
- If the recipient already has a project by that name, pass `--as NAME` — names
  are unique within an account, and this is what the project is called in theirs.
- The recipient's account has to be able to carry it: not suspended, has a card
  or a balance, and under their project limit. The refusal comes back to the
  owner with the fix in it — those are things the two of them sort out before
  anybody clicks.
- `gg unshare` on the person an offer was made to **withdraws the offer too** —
  an offer only ever goes to a member, so taking the access away takes the offer
  with it.
- **Never offer a project the user has not explicitly asked you to hand over.**
  This is the one command that ends with them not paying for something they no
  longer own, and no amount of context makes it inferable.

## Diagnosing

Start from the symptom, not from the logs.

| symptom | first suspect | what to run |
|---|---|---|
| a service never becomes ready | the declared port; the app binds elsewhere or on `127.0.0.1` | `gg status`, then redeploy on the port the app really binds |
| `ImagePullBackOff` | the image is not there — a copy that never finished, or a tag that does not exist | `gg history`, then push or copy the image again |
| the container starts and exits | a missing environment variable, or a crash | `gg logs shop/web` |
| killed with no log output | out of memory | `gg status` names the size to move to |
| one service hangs calling another | a missing `gg deps` edge — an undeclared call is dropped, not refused | `gg deps ls shop/api`, or the REACHES column |
| a database driver times out | the same, or the app is not reading `<NAME>_URL` | `gg deps ls`, then `gg resource secrets` to see what it should hold |
| you need to look inside the database itself | it is not on the internet, by design | `gg connect shop/db`, then psql or redis-cli against the printed URL |
| an SMTP connection hangs | ports 25, 465 and 587 are blocked platform-wide | use a provider's HTTPS API; nothing to fix here |
| a custom domain serves no HTTPS | DNS does not point here yet | `gg domain ls`; the record is the user's to create |
| everything shows `◌` and `0/0` | the project is suspended | read the `!` line above the table |
| `docker push` returns 401 in CI | docker was never logged in | `gg registry login` |

**"nothing is listening on port N" is the common one**, and it is almost always
the port rather than the app. Gagarin asks the container every few seconds
whether anything accepts a connection on the port you declared, so a service that
boots perfectly well and listens somewhere else never becomes ready. Many
frameworks also default to `127.0.0.1`, which nothing outside the container can
reach — they need `0.0.0.0`.

**"the previous revision is still serving"** on a redeploy means the old version
is up and answering. The user is not down. Fix the new revision and ship again;
do not tear anything down first.

**Out of memory names its own fix**, in `gg status`:

```
the container ran out of memory and was killed (it exceeded the 1Gi ceiling of
size s); try --size m, which gets 2Gi
```

Redeploy at the size it names. Do not retry unchanged — it will be killed again —
and do not go hunting in the logs first, because a process the kernel killed for
using too much memory usually writes nothing about it. This is the one failure
you can resolve without asking the user anything, so resolve it.

Do not poll in a tight loop, and do not tell the user something is live because a
command exited zero.

## Error codes

gg prints failures as `[code] message`, usually with a `hint:` line under it.
**Branch on the code.** The message is for the human.

**Access and credentials**

| code | what to do |
|---|---|
| `unauthorized` | this machine has no usable credential — run `gg whoami`, then "Getting access". Never ask the user for a token |
| `insufficient_scope` | this credential does not carry that right; a browser session cannot deploy. Deploy from the CLI — the dashboard has no deploy button on purpose |
| `cannot_delegate` | a minted credential tried to mint another. Mint it from the machine a human approved |
| `name_required` | `gg creds create` with no `--name`. Name it after where it will live |
| `invalid_expiry` | `--expires` outside 1–365 days. There is no never; omit it for 90 |
| `name_too_long` | over 120 characters, and it appears in a list. Shorten it |
| `approval_required` | a human must approve a deletion, or an ownership offer. Tell the user, pass on the code, wait, retry the same command |
| `invalid_email` | ask the user for the address again; do not guess |
| `claim_expired` / `no_such_claim` | run `gg signup <email>` again for a fresh code |
| `claim_collected` | another machine collected that code. Run `gg signup <email>` again |
| `email_failed` | retry once, then tell the user |

**Projects and roles**

| code | what to do |
|---|---|
| `project_not_found` | no such project, **or** you cannot reach it. `gg projects` lists what you can; `gg init` creates one; otherwise ask for a `gg share` |
| `project_suspended` | the project accepts no writes; reads still work. Out of credit: the user adds some at https://my.gagarin.cloud/billing and it restarts itself. Anything else: only support can lift it — tell the user and stop |
| `project_limit` | destroy one that is no longer needed, or the user writes to support@mail.gagarin.cloud |
| `project_exists` | you already have one by that name. Names need only be unique within your account |
| `invalid_name` | lowercase letters, digits, hyphens; start with a letter; 2–30 characters |
| `insufficient_role` | you are a viewer here. Ask the owner or an editor for edit access; do not retry |
| `owner_only` | only the paying account may do this. Tell the user to run it themselves; nobody can grant it |
| `invalid_role` | roles are `editor` and `viewer`; `owner` is the account that pays |
| `owner_not_a_member` | that address already owns the project; nothing to do |
| `member_not_found` | `gg members <project>` shows who has access |

**Handing a project over**

| code | what to do |
|---|---|
| `not_a_member` | share it first: `gg share P EMAIL`, then offer it |
| `already_owner` | they already own it; nothing to do |
| `name_taken_there` | they have a project by that name. Re-run with `--as NAME` |
| `recipient_suspended` | their account is stopped; they add credit, then you offer again |
| `recipient_cannot_pay` | no card and no balance there; the project would be suspended on arrival. They fix it at https://my.gagarin.cloud/billing |
| `recipient_project_limit` | their account is full; they destroy one, or write to support@mail.gagarin.cloud |
| `no_offer` | nothing is pending. If it was accepted, the project is theirs and only they can offer it back |
| `project_suspended` on a transfer | a project suspended in its own right cannot change hands. Only support can lift that |

**Services, images and deploys**

| code | what to do |
|---|---|
| `image_required` | `gg push` one first, or use `gg ship` |
| `image_not_yours` | the image is not in this project's registry space. Build or push into this project, or `gg registry copy` it in |
| `invalid_digest` | pass what `docker push` reported, or leave it out |
| `invalid_port` | set the port the container actually listens on |
| `invalid_kind` | `kind` is absent for a service or `job` for a job; nothing else exists |
| `job_has_no_port` / `job_has_no_volume` | drop the field: a job listens on nothing and keeps nothing. Durable data goes in a resource it reaches |
| `not_a_job` | that name is a service; a job needs a name of its own |
| `invalid_volume` | an absolute path inside the container, e.g. `/var/lib/postgresql/data` |
| `volume_immutable` | a volume is set once and never moves or resizes. Keep it, or destroy the service and deploy again — which throws the data away |
| `invalid_size` | sizes are `s`, `m`, `l`; the message names the account's cap |
| `no_such_revision` | `gg history` lists the ones it had |
| `nothing_to_roll_back_to` | deployed only once. Not a bad call, just nothing to do |
| `not_a_service` | that name is a resource or a job — you tried to deploy a service over it, give it an address, or roll back a managed resource whose credentials gagarin mints (an external can be rolled back) |
| `not_a_resource` | that name is a service. `gg status` shows which is which |

**The graph**

| code | what to do |
|---|---|
| `invalid_needs` / `invalid_deps` | a blank name, a service naming itself, or a job named as something to reach — a job listens on nothing. Correct them; `gg deps ls` shows what is declared |
| `no_such_service` | a name in `gg deps add` or `--deps` is neither a service nor a resource here. `gg status` lists them; create it first |
| `service_in_use` | something still declares it needs this. The refusal names what; `gg deps rm` that edge first |

**Resources**

| code | what to do |
|---|---|
| `no_such_resource` | `gg status <project>` lists what it has |
| `unknown_resource_type` | `postgres`, `qdrant`, `valkey` or `external` |
| `wrong_resource_type` | another type already holds that name. Destroy it — which throws its data away — or pick another name |
| `invalid_storage` | 1 to 100 GB |
| `no_storage` | this type has no volume: drop `--storage` |
| `no_size` | an external runs nothing: drop `--size` |
| `invalid_env` | write `API_KEY`, not `OPENAI_API_KEY` — the prefix is the resource's name |
| `already_exists` | restating an external. `gg resource rotate` is how values change |
| `env_required` | an external's values are the user's; pass `--set K=V` for one of them, `--env-file` for all |
| `conflicting_env` | `--env`/`--env-file` replace the bundle and `--set`/`--unset` amend it: use one or the other |
| `no_such_key` | that external does not publish the key named in `--unset`. `gg resource secrets` lists what it does — and the name goes in without the resource's prefix |
| `no_env` | this type mints its own credentials, so there is nothing to pass or amend; `gg resource secrets` reads them |
| `rotate_failed` | **nothing changed** — the old credential still works. Check `gg status` for a resource that is not running, then retry |
| `cannot_rotate` | no credentials recorded to replace; worth reporting as a bug |
| `not_tunnelable` | an external runs nothing, so there is nothing to tunnel to; its values are `gg resource secrets` |
| `tunnel_unavailable` | no running pod on the far end. `gg status` says why, then run `gg connect` again |

**Backups**

| code | what to do |
|---|---|
| `backup_unsupported` | only postgres has backups. If the user needs durability, the data belongs in postgres |
| `backup_unconfigured` | this gagarin runs without a backup bucket. Report it to the user |
| `restore_target_not_empty` | a restore only fills a NEW resource. Create one rather than reusing a live name |
| `backup_mismatch` | that key belongs to another project. Never restore across projects |
| `no_backups` | the nightly pass takes the first one; `gg resource backup` takes one now |
| `backup_list_failed` / `backup_failed` / `restore_failed` | the resource must be running — check `gg status`, then retry once |

**Addresses**

| code | what to do |
|---|---|
| `invalid_domain` | a hostname the user owns: no scheme, no port, no path |
| `domain_taken` | another service holds that name. The refusal does not say who |
| `domain_attached` | release the custom names first, or their DNS points at a host that no longer answers |

**The platform itself**

| code | what to do |
|---|---|
| `apply_failed` | desired state saved, cluster update failed. Retry the same command; it is idempotent |
| `cluster_error` / `internal_error` / `eject_failed` | not your call's fault. Report it to the user |
| `logs_unavailable` | no running pod yet. Check `gg status` first |
| `timeout` | retry once; if it persists, tell the user |
| `rate_limited` | wait a minute. Never retry in a tight loop |
| `body_too_large` | large values belong in a resource, not in a deploy call |
| `no_such_route` | you are calling the API directly and got the path wrong. Use `gg`, the only supported client |

## Destroying things

```
gg destroy shop        the project, and everything in it
gg destroy shop/web    one service
gg destroy shop/db     one resource, and its data
```

You do not say which of the last two a name is; gg asks the platform, which
already knows.

**This will be refused the first time, and that is not a bug.** Your credential
can deploy but not delete, so destroying anything needs a human every time the
approval window has lapsed. You get `approval_required`, and gagarin emails the
account owner. Then:

1. Tell the user what you are about to delete, and that you have asked them to
   approve it by email. Pass on the code from the `fix_hint`.
2. Wait for them to say they clicked it. Do not poll in a loop.
3. Run the same command again.

**Ask the user before *requesting* the approval**, not just before retrying — an
approval email for a deletion they never asked for is alarming. Only a project's
owner can destroy the project; an editor gets `owner_only`, and no amount of
approval changes that, because the answer is "ask the person paying for it".

If a deploy went wrong, prefer fixing it with another `gg ship`. Destroying and
recreating a project loses its data and its URL.

## Leaving

```
gg eject shop -o project.yaml
```

Writes the Kubernetes manifests for the whole project — namespace, and per
service its Deployment, Service, Ingress, NetworkPolicy, environment Secret and
volume claim. These are not a description of what gagarin runs; they are the
objects it runs, so `kubectl apply -f` on any cluster with an ingress controller
reproduces the project.

Owner only, because the file holds every service's environment in the clear. It
is written mode 0600 and you should treat it as a credential.

Three things it deliberately does not contain, all explained in its own header:
the **images**, which are still in gagarin's registry and have to be pulled and
pushed somewhere the user controls; the **registry pull secret**, which is a
live credential; and the values from any **external resource**, which are
third-party keys somebody else issued — they come out as a placeholder naming
the resource, and the header lists exactly which variables to fill in. Volume
claims come back empty — data has to be taken out of the running service.

A minted credential — a database password — *is* in the file, and the asymmetry
is the point: that password is only a risk against a database in the same file,
and an export without it does not come up. A third-party key stays live wherever
the file ends up. `gg eject shop --with-secrets -o project.yaml` includes them,
for a migration happening now into a file that gets deleted; say what that means
before running it with the flag.

Offer this without being defensive when somebody asks what happens if gagarin
goes away. It is a real answer and it is meant to be used.

## Things gagarin deliberately does not do

Not missing features. Do not attempt them and do not suggest workarounds:

- deploy from a git repository or a git URL
- read deployment configuration from a file in the repo — `--env-file` supplies
  values, and is the only file gagarin will ever read
- interpolate variables into each other
- pull images from Docker Hub, GHCR or any registry other than gagarin's at run
  time (`gg registry copy` brings one in first)
- expose Kubernetes, cloud provider or networking primitives
- open the mail ports
- infer a project from the current directory, or from anything else
