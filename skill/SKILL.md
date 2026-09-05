---
name: gagarin
description: Deploy and operate applications on gagarin. Use when the user asks to deploy a project, check what is running, read service logs, or tear a deployment down. Gagarin runs container images on managed infrastructure; the user never needs to know about Kubernetes, ingress, TLS, or the underlying cloud.
---

# Deploying with gagarin

Gagarin runs your services. You interact with it through the `gg` CLI, which is a
thin wrapper over the gagarin API. There is exactly one way to change anything:
call the API. There is no manifest file, no config file, and nothing to commit.

## The model

- A **project** owns everything. One project per application. It has a **name**
  you chose, unique only within your account, and an **id** gagarin generated —
  eight characters — which is what image paths and public hostnames are built
  from. Commands take either.
- **Every command is asynchronous.** You submit a desired state and gagarin
  converges on it. Nothing blocks waiting for the cluster, so a command that
  returns successfully means *the demand was recorded*, not *the thing is
  running*. Those are different claims and gg will only make the first one.
  `gg status <project>` is where you find out about the second — it reads the
  cluster, and it is the only thing that can answer "is it up".
- **Everything is named for its project, always.** A project is `shop`; a service
  or a resource in it is `shop/web`. Nothing is inferred from the directory you
  are standing in — `gg` will not guess a project, and a command that does not
  name one is refused with the shape it should have had. Do not go looking for a
  way to set a default; there is none, on purpose.
- A project owns **services**. A service is a container image that runs.
  - A service is **private until you give it an address**. Private means no URL,
    reachable from other services in the same project by name —
    `http://worker:8080` — and only by the ones that declared they need it.
  - `gg domain add <project>/<service>` puts it on the internet, at an HTTPS
    address gagarin generates. Run it for the service the user's browser is meant
    to open, and for nothing else. Do not expose a database, a worker or an
    internal API "so it can be tested": ask the user before putting anything on
    the internet they did not ask to put there.
  - **A deploy never changes this.** `gg ship` cannot make a service public and
    cannot make it private — there is no flag for it, and there used to be. A
    service that has an address keeps it across every deploy; one that does not
    stays private until somebody says otherwise.
- A project owns **resources**: things gagarin provides rather than things you
  built. `postgres`, `ferretdb` (MongoDB-compatible) and `valkey` (redis
  protocol). You name it and say how big; every other decision is the
  platform's. See "Databases and caches" below.
- Gagarin runs images **only from its own registry**. Building, pushing and
  deploying are three separate verbs — `gg build`, `gg push`, `gg deploy` — and
  `gg ship` is all three at once, which is what you want most of the time. To run
  something you did not build, copy it in first:
  `gg registry copy shop/caddy caddy:2-alpine`. Do not write a one-line
  Dockerfile that only says `FROM`; that is the same thing, worse.
  **If a resource type exists, use it.** `gg resource add <project>/db postgres` beats
  copying a postgres image in and deploying it as a service — that was the old
  workaround, and it is now the wrong answer for postgres, ferretdb and valkey.
  **For anything else it is still the right answer**: gagarin does not have a
  type for DuckDB, Cassandra, ClickHouse or a vector database, and a service
  with a volume is the ordinary way to run one, not a hack. See "Anything we
  do not have a type for".

Gagarin itself holds the source of truth for all of this. Do not try to write
config into the repository; it will not be read.

## Installing gg

Run `gg whoami`. If the command is found — even if it reports no credentials —
`gg` is installed and you can skip to the next section.

If the shell reports that `gg` does not exist, install it. Prefer whichever of
these the machine can already run, in this order:

```
brew install gagarin-cloud/tap/gg
go install github.com/gagarin-cloud/gg@latest
```

The first needs Homebrew, the second needs Go. If the machine has neither, take
a released binary for its platform from
https://github.com/gagarin-cloud/gg/releases — every release publishes
checksums, and you should verify them. **Do not pipe a script from a URL into a
shell**, do not download from a host you guessed, and do not carry on without
`gg`: every step below goes through it.

If `go install` succeeds but the shell still cannot find `gg`, its bin directory
(`go env GOPATH`/bin) is not on `PATH`. Say so and let the user fix their profile
— do not edit their shell configuration yourself.

Once installed, run `gg skill install` to refresh this skill from the binary, so
what you are reading matches the CLI you now have.

## Getting access

Run `gg whoami`. If it answers with an account, this machine is already
authorised and you can skip this section. There is nothing to export and no token to ask the user for —
if you find yourself wanting a secret, you are doing this wrong.

If it says the machine has no credentials, sign up. Any address works: there is
no list to be on and nothing to wait for. One email, one button, and the account
starts with $5 on it; no card is asked for.

1. **Ask the user for their email address.** Do not guess it, and do not use an
   address you found in the repository or in git history — a deploy that lands in
   a stranger's account is worse than no deploy.
2. `gg signup <email>`
3. **Tell the user to press the button in the email.** If they have no account
   yet, that press creates one; either way it authorises this machine. Say the
   code `gg signup` printed, so they can check the email is the one you
   triggered.
4. `gg auth --claim <code>` — this waits for the press, then stores credentials
   in `~/.config/gagarin/credentials.json`.

Signing up and authorising another machine are the same request, and the
response is the same whether or not the address already has an account. The
moment an account is created it gets its $5 starting balance and the address
joins gagarin's customer list; https://gagarin.cloud/privacy says what that list
is for. If the user asks, that is the answer.

The credential this produces can deploy, read status, and read logs. It **cannot
delete anything**: see "Destroying things" below.

You never handle the credential yourself, and you should not read that file or
echo it anywhere. If a command needs authorisation it will say so.

`gg auth` also logs `docker` in to gagarin's registry, using the credential it
just stored — there is no second account and nothing to type. If that step is
skipped because `docker` was not installed yet, run `gg registry login` once the
machine has it.

### Giving CI a credential

**Do not run `gg signup` in CI, and do not copy this machine's credential into
it.** A pipeline gets its own, minted from the one you already hold:

```
gg creds create --name "github actions: acme/web"
```

The secret is printed on stdout, alone on its line, and everything else goes to
stderr — so it pipes:

```
gh secret set GAGARIN_TOKEN --body "$(gg creds create --name "github actions: acme/web" 2>/dev/null)"
```

CI reads `GAGARIN_TOKEN` from the environment and needs nothing else: no
`gg auth`, no credentials file, no home directory. In a workflow:

```yaml
env:
  GAGARIN_TOKEN: ${{ secrets.GAGARIN_TOKEN }}
```

**It is shown once.** gagarin keeps a hash, not the secret. If it is lost, revoke
it and mint another — there is no command that reads one back.

**It is deliberately weaker than yours.** It can deploy. It cannot destroy, so a
pipeline holding it cannot delete a project, a service or a database; no flag
turns that on. It expires (90 days by default, 365 maximum, and there is no
never). And it cannot mint another, so a leaked one cannot issue its own
replacements while you are revoking it.

Name it after where it will live. `gg creds` is a list somebody reads
months later deciding what is still wanted, and "token" tells them nothing.

```
gg creds              what has access, and which are minted
gg creds revoke 7     take one away, immediately, no approval needed
```

If you are ever tempted to set `XDG_CONFIG_HOME` to a scratch directory and run
the signup flow again to get a second credential: that used to be the only way
and it is not any more. Use `gg creds create`.

## Deploying a project

1. **Confirm there is a Dockerfile** for each service you intend to deploy.
   Gagarin deploys images, so a service without a Dockerfile cannot be
   deployed. If one is missing, write it — that is a normal part of this job.
   Do not set a platform or architecture yourself: gagarin reports what its own
   nodes run and `gg deploy` builds for that.

2. **Create the project** once:
   ```
   gg init <project>
   ```

3. **Ship each service.** `gg ship` builds the current directory, pushes it, and
   runs it — the whole job in one command. Run it from the directory containing
   that service's Dockerfile, or point `--context` at one:
   ```
   gg ship <project>/worker:8080
   gg ship <project>/web:8080 --env WORKER_URL=http://worker:8080
   gg domain add <project>/web
   ```
   The last line is what puts `web` on the internet. It is a separate command
   because an address outlives any one image: a deploy can neither create one nor
   take one away, so nothing goes dark because a flag was forgotten.
   The number after the colon is the port the container listens on. It defaults
   to 8080 when you leave it out; say it anyway when you know it, because a
   service answering on the wrong port is a hang rather than an error.

4. **Connect them.** Nothing in a project can reach anything else until you say
   so, and `gg deps` is what says so:
   ```
   gg deps add <project>/web worker
   ```
   This is not documentation — without it `web` cannot reach `worker` at all, and
   the failure is a hang rather than an error. See "Connecting services" below.
   Order does not matter here the way it used to: both services have to exist
   before you can declare an edge between them, so ship first and connect after.

   If what it reaches is a **resource**, this one call also hands `web` that
   resource's credentials, so there is nothing else to do — no reading a password
   and passing it to a deploy. `gg deploy --deps` and `gg ship --deps` declare it
   in the same call as the deploy, which is what you want for a service that
   cannot start without a database.

5. **Verify, and only then give the user the URL:**
   ```
   gg status <project>
   ```
   This step is not a formality. `gg ship` says nothing about where a service
   answers, deliberately: it does not decide that, and an address exists as a
   string — derived from the service name and the project id — long before
   anything is listening on it. `gg status` is the only thing that turns it into
   a fact. It lists every address under its service, and marks each service:

   - `●` **running** — the cluster is running what was asked for, and something
     is answering on the port that was declared.
   - `◐` **starting** — in flight. Pulling an image, booting, waiting on an
     ingress. The cluster has not given up, and neither should you.
   - `○` **failing** — either there is nothing in the cluster at all, or
     Kubernetes has stopped calling this a rollout in progress. Waiting will not
     fix it.

   `◐` and `○` both print the cluster's own explanation beside them. Read it
   before changing anything. `ImagePullBackOff` means the image is not there —
   usually a copy that never finished, or a tag that does not exist. A container
   that starts and exits is in `gg logs <project>/<service>`.

   **"nothing is listening on port N"** is the common one, and it is almost
   always the port rather than the app. Gagarin asks the container every few
   seconds whether anything accepts a connection on the port you declared, so a
   service that boots perfectly well and listens somewhere else never becomes
   ready. Check what the app actually binds and redeploy on that port. Note also
   that many frameworks default to `127.0.0.1`, which nothing outside the
   container can reach — they need `0.0.0.0`.

   If the service was already running and you redeployed it, an explanation
   ending **"the previous revision is still serving"** means the old version is
   still up and answering. The user is not down. Fix the new revision and ship
   again; do not tear anything down first.

   Do not poll in a tight loop, and do not tell the user something is live
   because a command exited zero.

### When to use the three steps instead

`gg ship` is build, push and deploy fused. Reach for them separately when you
want less than all three:

```
gg build <project>/web:v3 --context ./web    make an image, run nothing
gg push  <project>/web:v3                    publish it, release nothing
gg deploy <project>/web:8080 web:v3          release one that already exists
```

That is what CI wants — publish on every commit, release on some of them — and
it is the only way to run an image you did not just build, including one copied
in with `gg registry copy` and one you are rolling forward to by hand.

`gg build` invents a tag when you do not give it one. `gg push` will not: it
moves something that already exists, and "whichever one I built last" is not a
name.

## Environment variables

Set them individually, or read them from a plain `KEY=VALUE` file:

```
gg deploy <project>/web:8080 web:v3 --env-file .env
gg deploy <project>/web:8080 web:v3 --env-file .env --env DEBUG=false
gg ship   <project>/web:8080 --env-file .env
```

- `--env-file` is **not** picked up automatically. If the user has a `.env` and
  wants it used, pass the flag; never assume a file should be read.
- **What the container holds is this plus the connection variables of every
  resource it reaches.** Those are not set here and do not belong in a `.env` —
  the platform adds them from the dependency graph every time it starts the pod.
  A resource's variable wins over one of the same name set here, which is the
  only exception to the rule that a deploy decides a service's environment. See
  "Resources".
- `--env` flags override any file. Later `--env-file` flags override earlier ones.
- There is **no interpolation**: `B=${A}` sets `B` to the literal text `${A}`.
- Env is part of a service's desired state and is **replaced** on each deploy, not
  merged. To remove a variable, deploy again without it. To keep a variable, keep
  passing it.

The file supplies **values only**. It cannot name services, set ports, or say
what is public — that is not a limitation to work around, it is the design. If
you find yourself wanting to put deployment structure in a file, stop: gagarin
holds that itself, and the only way to set it is on the command line.

Environment is the *one* thing a deploy still replaces wholesale, and that is
deliberate: it is part of what a revision ran with, and it is what a rollback
puts back. Everything else about a service that could be lost by forgetting to
restate it — its dependencies, its domain, its volume — has been moved out of a
deploy for exactly that reason.

## How much CPU and memory a service gets

Every service and every resource runs at a **size**. There are three, and `s` is
what you get if you say nothing.

| | reserved | ceiling | price |
|---|---|---|---|
| `s` | 100m CPU / 256 MiB | 0.5 vCPU / 1 GB | $10/month |
| `m` | 1 vCPU / 2 GB | same — dedicated | $30/month |
| `l` | 2 vCPU / 4 GB | same — dedicated | $60/month |

```
gg deploy <project>/web:8080 web:v3 --size m
gg resource add <project>/db postgres --size m
```

**`s` is shared and `m` and `l` are dedicated.** An `s` service may burst to
0.5 vCPU and 1 GB but is only guaranteed the smaller reservation, which is why
it costs a third of `m`. An `m` or `l` service reserves exactly what it is
promised and is the last thing evicted when a node runs short.

**Omitting `--size` keeps the size the service already has.** It does not reset
it to `s`. So a redeploy of an `m` service stays `m` without you restating it —
and if you *want* to change a size, say so explicitly.

**Which to pick.** Start at `s`. It runs a small API, a worker, a static site,
and a database you are developing against. Move to `m` when something is killed
for running out of memory — see `oom_killed` below, which tells you when — or
when you already know the workload: a Next.js app doing server-side rendering, a
JVM, anything holding a large cache, or a postgres a real application depends on.
Do not reach for `l` speculatively; it costs six times `s` and one of them fills
most of a node.

**Changing a size restarts the service.** It is a new pod with new limits, so
treat it like a deploy: expect a few seconds of downtime for a service with no
replicas, and do not do it while something depends on it being up.

## Connecting services

A private service is reachable at `http://<service-name>:<port>` from inside the
same project — but **only by the services that declared they call it.**

Declare that with `gg deps`, on the service doing the calling:

```
gg deps add <project>/api db
```

`db` now accepts connections from `api` and from nothing else. Without that
declaration the address still resolves and the connection is simply never
answered.

**If the thing being reached is a resource, this also hands over its
credentials** — see "Resources" below. For an ordinary service it grants the
route and nothing else, because a service has no credentials to publish.

**This is the part that will waste your time if you skip it.** A call nobody
declared is not refused — it is dropped. So it does not fail fast with
"connection refused"; it **hangs until the client's timeout**, which for a
database driver can be 30 seconds or forever. A hang is the symptom of a missing
dependency far more often than it is a bug in the application.

So when a service hangs talking to another one, check `gg status` *first*. It
ends every service's line with what that service can reach:

```
api is a private service on port 4000, reaching db.
web is a public service on port 8080, reachable at https://…, reaching nothing else.
```

`reaching nothing else` is a statement of fact, not a warning — most services
legitimately need nothing. But if you expected a name there and it is missing,
that is your hang, and the fix is one command:

```
gg deps add <project>/web the-missing-one
```

The three verbs:

```
gg deps ls  <project>/api            what it reaches today
gg deps add <project>/api db cache   and these as well
gg deps rm  <project>/api cache      and no longer that one
```

`add` and `rm` are the only things that can *withdraw* an edge or declare one
wholesale. A deploy can add to the set — `gg deploy … --deps db`, and the same
flag on `gg ship` — which exists so that a service needing a database can be
declared and deployed in one call rather than starting into a window where it
cannot reach one. It only ever adds: a deploy that omits `--deps` leaves the
graph exactly as it was, which is what makes it safe where the old `--needs`
was not.

Two things about this are worth knowing:

- **The direction matters.** The declaration goes on the *caller*. If `api`
  queries `db`, it is `api` that needs `db`, never the other way round. Asking
  for it backwards is refused rather than quietly accepted.
- **Public URLs are not covered by this.** If one service calls another's public
  `https://` address, that arrives the same way a stranger's browser does and is
  allowed. The guarantee is about private, in-project addresses: *a private
  service is unreachable unless something declared it needs it.*
- **Withdrawing applies to new connections, not open ones.** `gg deps rm` closes
  the path immediately for anything that connects afterwards, but a client
  already holding a keep-alive connection — which most HTTP clients and every
  database driver pool do — can keep using it until it reconnects. This is how
  Kubernetes network policy works and gagarin cannot change it. If you need the
  old path gone *now*, redeploy the caller: a new pod has no old connections.
  Do not read a still-working request as a failed withdrawal without checking
  `gg status` first, which reports what the platform is actually enforcing.

**A deploy can never take any of this away.** That matters: this used to be a
`--needs` flag on `gg deploy`, replaced wholesale on every deploy, so a redeploy
that failed to restate a dependency silently withdrew it — and because an
undeclared call hangs rather than failing, the service did not break, it went
quiet. If you are working from memory or from an older skill and reach for
`--needs`, it is gone. `--deps` is not it coming back: it adds and never
replaces, so forgetting it costs nothing. Deploy as often as you like; the graph
stays where you put it.

To take a service away entirely, `gg destroy <project>/<name>`. It is refused
while anything still needs it, and names what does — `gg deps rm` that first.

### Reaching the outside world

Outbound is unrestricted, with **one exception you need before you build on
it: the mail ports — 25, 465 and 587 — are blocked.** A connection to any of
them times out, from every service, and it cannot be opened per-account.

So **do not build anything that speaks SMTP.** If the user wants their
application to send email — a signup confirmation, a password reset, a
notification — reach for a provider's HTTPS API instead: Resend, Postmark,
SendGrid, SES, any of them. That is a normal way to send mail, not a
workaround, and it is what gagarin itself does.

The reason is worth passing on if the user asks: a service that can open those
ports can relay spam if it is ever compromised, and a hosting account that
relays spam gets suspended — which would take every other project on the
platform down with it.

If you find yourself debugging a hanging SMTP connection, stop. It is not the
user's code and it is not DNS. It is this.

## Undoing a deploy

Every deploy is recorded. `gg history <project>/<service>` lists them newest first, with a
revision number, the image, when it happened and who asked for it; the live one
is marked.

```
gg rollback <project>/<service>            put the previous deploy back
gg rollback <project>/<service> --to 3     put a particular revision back
```

Three things about this are worth knowing before you reach for it.

- **A rollback is a deploy.** It goes through the same write gate, and the
  platform records it as a *new* revision that names the one it restored.
  Nothing is removed from the history, so rolling back and then changing your
  mind is another rollback rather than a lost record.
- **It restores the environment too**, exactly as that revision had it. A
  variable added since is gone after the rollback, because it was not part of
  what you went back to.
- **It does not restore the dependency graph, the domain, or the volume.** Those
  are standing declarations about the shape of the project rather than parts of
  the artifact, and putting yesterday's image back says nothing about them. A
  dependency you added since the revision you are restoring survives the
  rollback.
- **It is refused across a change of volume**, with `volume_immutable`. A volume
  is set once, at the deploy that creates a service, so reverting one would
  abandon or destroy data. Deploy the configuration you want instead.

Prefer a rollback to a corrective deploy when something you just shipped is
broken and you do not yet know why: it is one call, it restores a state that
provably ran, and it leaves the evidence intact for afterwards.

## Leaving

```
gg eject <project> -o project.yaml
```

Writes the Kubernetes manifests for the whole project — namespace, and per
service its Deployment, Service, Ingress, NetworkPolicy, environment Secret and
volume claim. These are not a description of what gagarin runs; they are the
objects it runs, so `kubectl apply -f` on any cluster with an ingress controller
reproduces the project.

Owner only, because the file contains every service's environment in the clear.
It is written mode 0600 and you should treat it as a credential.

Two things it deliberately does not contain, both explained in the file's own
header: the **images**, which are still in gagarin's registry and have to be
pulled and pushed somewhere the user controls, and the **registry pull secret**,
which is a live credential to our registry. Volume claims come back empty — data
has to be taken out of the running service.

Offer this without being defensive when somebody asks what happens if gagarin
goes away. It is a real answer and it is meant to be used.

## Databases and caches

```
gg resource add <project>/db postgres
gg resource add <project>/docs ferretdb
gg resource add <project>/cache valkey
```

That is the whole of it. There is no image to choose, no version to pass, no
Dockerfile — a resource is provisioned rather than deployed, and the only thing
you may set is how big its storage can be (`--storage 20`, default 10GB, and it
can be grown later, never shrunk).

There is a fourth type, `external`, which is not a database and not something
gagarin runs at all — it is how a third-party API key becomes a node on the
graph. See "Third-party APIs" below.

**The types, and the differences that matter:**

| type | what it is | storage |
|---|---|---|
| `postgres` | PostgreSQL 17 | on a volume; survives restarts |
| `ferretdb` | MongoDB-compatible document DB | on a volume; survives restarts |
| `valkey` | in-memory store, redis protocol | **none — a restart loses everything** |
| `external` | a third-party API, run by nobody here | **none — it runs nothing at all** |

The first three are things gagarin runs for you. `external` is the odd one and
is covered in "Third-party APIs" below: no container, no port, no size, no
backups — it holds credentials and publishes them, and that is all it does.

The types are named for what actually runs, not for the products they stand in
for — an honesty you can pass on when somebody asks.

`ferretdb` is **FerretDB**, the Apache-licensed MongoDB alternative, storing
into Postgres. It speaks the MongoDB wire protocol: `mongodb://` URLs, the
official drivers, Mongoose and every ODM work unchanged, and for ordinary CRUD,
indexes and aggregation you cannot tell. It is not MongoDB — gagarin does not
run MongoDB because MongoDB's licence (SSPL) forbids offering it as a service —
so an app leaning on an exotic corner of the surface may notice; if one does,
run real MongoDB yourself as a service with a volume (next section), where the
licence is not gagarin's problem and not yours either, because serving your own
app is not "offering MongoDB as a service".

`valkey` is a cache, not a database. There is no volume, so `--storage` is
refused (`no_storage`) rather than quietly ignored, and anything in it is gone
when the pod restarts — which happens on a node drain, not only when somebody
asks. Do not put a session store there that a user would notice losing, and do
not tell anyone their data is safe in it. **Valkey** is the BSD-licensed fork
of Redis (whose licence also forbids hosting it), and this changes nothing you
can observe: `redis://` URLs, `redis-cli`, and every client library work
unchanged.

### Third-party APIs: the `external` type

An OpenAI account, a Stripe key, a bucket somewhere else. Not something gagarin
runs — something your services need a credential for. Make it a resource anyway:

```
gg resource add <project>/openai external --env-file .env.openai
gg deps add <project>/bot openai
```

An `.env.openai` holding `API_KEY` and `BASE_URL` makes `bot` hold
`OPENAI_API_KEY` and `OPENAI_BASE_URL`. Same naming rule as every other type:
the resource's name, then the key. **Write the keys without the prefix** —
`API_KEY`, not `OPENAI_API_KEY`, which is refused rather than doubled.

**Why bother, instead of `--env OPENAI_API_KEY=…` on the deploy?** Because a key
on a deploy is a copy. Three services using one key means three copies, rotating
it means three deploys, and a deploy you forget leaves a service authenticating
with a revoked key. As a resource it is one row, and rotating it is one command:

```
gg resource rotate <project>/openai --env-file .env.openai.new
```

Every service holding the old value is restarted with the new one. Nothing else
to do — no deploys, and no working out which services were affected, because the
command tells you which ones it rolled.

Note it is `rotate`, not `add`. Restating an external with `gg resource add` is
**refused** once it exists, and that refusal is deliberate: restating it from an
old `.env` file would have rolled the key *backwards*, and the failure would have
been an authentication error nobody would connect to the command they ran.

**It runs nothing.** No container, no port, no size, no storage, no backups,
nothing to become ready. `gg status` shows it with a `◆` rather than a state dot,
and a line under it saying what it publishes.

**Use `--env-file`, not `--env`.** A key passed on the command line is in the
shell history and in the transcript of every agent session that ran it. If the
user hands you a key in chat, write it to a file first, use the file, and tell
them the file has a live credential in it.

**Declaring an external does NOT restrict egress.** This is the one thing not to
get wrong when you explain it. Everything in a project can already reach the
whole internet; the mail ports are the only exception, and that is it. What the
declaration gives you is the credentials and a line on the graph saying which
services use them. It is **not** a firewall, and an undeclared service can still
call `api.openai.com` — it just has no key. `gg status` marks these edges with a
`◆` for exactly this reason: `openai◆, pg` in a REACHES column means one enforced
edge and one that is an inventory note.

**What it is genuinely worth:** the project's dependencies are legible. `gg
status` answers "what does this thing actually depend on, including things we do
not run" — and destroying an external is refused while anything still declares
it, so a key cannot be dropped out from under a running service.

### Rotating credentials

Every resource type rotates with one command, and everything holding the old
credential is restarted with the new one:

```
gg resource rotate <project>/db                                a database
gg resource rotate <project>/openai --env-file .env.new         an external
```

**Who supplies the new value is the only difference.** For `postgres`,
`ferretdb` and `valkey` gagarin mints one and `--env` is **refused** — a password
you chose is one the running server has never heard of. For an `external` the
values are yours, so `--env` or `--env-file` is **required**.

**The costs are not the same, and a user will want to know before you run it:**

| type | what happens | downtime |
|---|---|---|
| `postgres` | the running server is told immediately | none |
| `ferretdb` | the same, in the Postgres it stores into | none |
| `valkey` | the pod is replaced, because the password is read at startup | **the cache is emptied** |
| `external` | nothing of ours runs, so nothing of ours restarts | none |

In every case the *dependents* restart, because their environment changed. That
is the point of the command.

**If it fails, nothing changed.** The old credential is still in use and the
command is safe to run again. Say that to the user rather than leaving them
wondering what state a half-failed rotation left behind — there is no half.

**Rotate after a leak, and say so plainly.** If a key has been in a chat log, a
commit, or a screenshot, rotating is the fix and it takes one command. For an
external, note that gagarin forgets the old value but cannot *revoke* it — that
is a thing to do in the third party's own console, and the user has to do it.

### Anything we do not have a type for

Three types will never cover everything, and the list of things somebody needs
is longer than any list gagarin will carry. Run it as an ordinary service with a
volume:

```
gg registry copy <project>/qdrant qdrant/qdrant
gg deploy <project>/vectors:6333 qdrant --volume /qdrant/storage --volume-size 20
```

Everything else behaves the same way: a private address by name,
`gg deps add <project>/api vectors` from whatever talks to it, a volume that
survives restarts, and the same refusal to delete it while something still needs
it.

The only difference is who decides. For a resource the platform picks the image,
the version and the port, and carries them. Here you pick them, and upgrades,
tuning and consequences are yours.

So: **if a type exists, use the resource** — fewer decisions, fewer ways to get
it wrong. If it does not, this is not a workaround, it is the normal path.

**Connecting to it is one command, and it is the same one that opens the route.**

```
gg deps add <project>/api db
```

That declares the dependency *and* puts the database's connection variables in
`api`'s environment. `api` can now reach `db` and authenticate with it, and the
command prints which variables arrived:

```
api reaches db, and nothing else.

api now holds DB_DATABASE, DB_HOST, DB_PASSWORD, DB_PORT, DB_URL, DB_USER
  in its environment, from the resources it reaches. Values: gg resource secrets.
```

Or declare it on the deploy, so the service never starts without them:

```
gg deploy <project>/api:8080 api:v3 --deps db
```

**The variables are named after the resource, not the protocol.** The rule is the
resource's own name, upper-cased with dashes as underscores, plus one of six
suffixes:

| suffix | postgres | valkey | ferretdb |
|---|---|---|---|
| `<NAME>_URL` | `postgres://…?sslmode=disable` | `redis://:pass@…` | `mongodb://…` |
| `<NAME>_HOST` | ✅ | ✅ | ✅ |
| `<NAME>_PORT` | 5432 | 6379 | 27017 |
| `<NAME>_USER` | ✅ | — | ✅ |
| `<NAME>_PASSWORD` | ✅ | ✅ | ✅ |
| `<NAME>_DATABASE` | ✅ | — | ✅ |

So a postgres called `db` publishes `DB_URL`; one called `orders-db` publishes
`ORDERS_DB_URL`; a valkey called `cache` publishes `CACHE_URL`, and no
`CACHE_USER`, because valkey has no users. A type omits the suffixes it has no
answer for rather than publishing them empty.

Two things follow, and both are the point. A service can reach two databases
without their variables colliding — which the old protocol-named scheme could
not express. And there are six suffixes to learn instead of a different set per
type.

**These names are not the ones your framework expects, and that is deliberate.**
There is no `DATABASE_URL`, no `PGHOST`, no `REDIS_URL`, no `MONGODB_URI`. The
first thing to try is to have the application read `DB_URL` — one line in the
app, and nothing about it can go stale.

If you genuinely cannot change the application, read the value and pass it under
the name it wants:

```
gg deploy <project>/api:8080 api:v3 --deps db \
  --env DATABASE_URL="$(gg resource secrets <project>/db --format json | jq -r .env.DB_URL)"
```

Note what that is: **a copy**, and the only stale thing in the model. `$DB_URL`
on its own would not work here — the injected variables exist inside the pod, not
in the shell running `gg`, which is why the value has to be read out explicitly.
Say so to the user rather than leaving a second copy of a password in a deploy
nobody remembers making.

**Injected variables outrank your own.** If you set `DB_URL` yourself on a deploy
and the service also reaches a resource called `db`, the resource's value wins.
It is the only place in gagarin where something beats what a deploy said, and the
reason is that only the resource knows the real password — a shadowed `DB_URL`
would fail as a connection error pointing nowhere near its cause.

**Nothing is copied into the service's stored environment.** The platform unions
these in every time it renders the pod, from the graph as it stands then. So they
cannot go stale, no redeploy can forget them, and a rollback restores the image
and the environment you deployed with — never an old password. `gg deps rm` takes
them away again just as surely.

**Reaching still implies declaring.** Holding the credentials is not enough: an
undeclared service with a correct password still cannot reach the port, because
the network policy is the boundary and it is a separate thing from the
environment. You cannot get the credentials without the declaration that opens
the route, so this is no longer a trap — but do not conclude from a working
password that the route is open for some *other* service you did not declare.

**`gg resource secrets` prints the values**, and you do not need it to connect a
service in the same project — that is `gg deps add`. Reach for it when something
else needs them: a `psql` on the user's laptop, a client running outside gagarin,
or checking what an application is actually being handed.

```
gg resource secrets <project>/db                 # KEY=VALUE lines
gg resource secrets <project>/db --format json   # for jq
```

It prints the same variables the platform injects. Treat the output as a live
credential: do not echo it into a chat, a commit, or a summary for the user.

Things worth knowing before you promise a user anything:

- **Postgres is backed up nightly; nothing else is.** A dump is taken every
  night and kept **fourteen days**. `gg resource backups <project>/db` lists
  them; `gg resource backup <project>/db` takes an extra one right now, which
  is the correct move immediately before a risky migration. Valkey is a cache
  and keeps nothing across a restart, by documented decision; ferretdb has no
  backups yet. There is still no point-in-time recovery and no failover — say
  so plainly if a user asks whether their data is safe, rather than implying
  an SLA nobody is on the hook for.
- **A restore creates a NEW resource — it never overwrites an existing one.**
  This is the platform's rule, not a convention: the engine refuses to restore
  into any database that already holds data, which is exactly why a restore
  needs no human approval and works at three in the morning. The recovery
  runbook, in order:

  ```
  gg resource restore <project>/db2 --source db     # new resource, filled from db's newest dump
  gg deps add <project>/api db2                     # hands api DB2_URL and the rest
  gg deps rm  <project>/api db                      # and stop it reading the old one
  gg destroy <project>/db                           # once everything reads from db2
  ```

  **The variables change with the name.** The restored resource is called `db2`,
  so it publishes `DB2_URL`, not `DB_URL`. An application reading `DB_URL` from
  its environment will not find one after the `gg deps rm`, so either name the
  new resource what the old one was called (only possible once the old one is
  destroyed) or expect to touch the application. This is the one place the
  per-resource naming costs something, and it is worth knowing before three in
  the morning.

  The old resource may already be destroyed when you restore — its backups
  outlive it by fourteen days, and `--source db` still finds them. Only the
  final `gg destroy` touches data that exists, and that is the one step that
  asks for a human's approval. An exact point in time instead of the newest:
  `--backup <key>` with a key from `gg resource backups`.
- **It is one instance on one volume.** Deploys restart it rather than rolling,
  so a restart is a brief outage.
- **Destroying it deletes the data**, and needs a human's approval like any
  other destructive act: `gg destroy <project>/db`. You do not have to say that
  it is a resource rather than a service — gg asks the platform, which knows.
- **The storage can grow but never shrink.** Restate the resource with a bigger
  `--storage` to raise the ceiling — `gg resource add <project>/db postgres
  --storage 40` on an existing `db` grows it and changes nothing else. Asking for
  a smaller number is refused with `volume_immutable`, because a volume cannot be
  made smaller: to get one, destroy it and create it again, which deletes the
  data.
- **Ferretdb's URL carries no `authSource`, and does not want one.** There is
  no `admin` database to authenticate against — FerretDB checks the credentials
  itself and takes them whatever database the driver names. If you are porting
  config that hard-codes `authSource=admin` for a previous MongoDB, drop it and
  pass `<NAME>_URL` as given. It publishes one URL, not the two spellings the
  MongoDB ecosystem never chose between — under a per-resource prefix neither
  `MONGODB_URI` nor `MONGO_URL` would have matched what a library looks for
  anyway.
- A resource cannot be deployed over, and cannot be rolled back — it has no
  deploy history. `gg deploy <project>/db` against a resource is refused with
  `not_a_service`, which is telling you the name is already a database. Nor can
  a resource declare dependencies: it is reached, it does not reach, and asking
  for it the other way round is refused for the same reason.

## Addresses

A service is private until it is given an address, and one command gives it
either kind:

```
gg domain add <project>/web                     an address gagarin generates
gg domain add <project>/web shop.example.com    a name the user owns, as well
```

The first is idempotent and instant: gagarin holds the wildcard record and the
wildcard certificate, so there is nothing to coordinate and nobody to wait for.
Run it for the service the user's browser is meant to open — and only that one.

**A name the user owns is two steps, and only the first is gagarin's.** That
command makes the service answer for the name. Making the name *resolve* here is
a DNS record the user creates at their registrar, and gagarin cannot do it for
them. The command prints the exact record — pass it on verbatim rather than
paraphrasing it.

**Nothing is served over HTTPS on that name until the record exists.** Let's
Encrypt proves control by fetching the domain over the internet, so a certificate
cannot be ordered before DNS points here. This is the normal first state, not a
fault. Do not treat it as one, and do not retry the command hoping it resolves —
it will not, because the missing piece is on their side.

`gg status <project>` says which of the two of you it is waiting on:

| reading | who acts |
|---|---|
| waiting for DNS | **them** — the record does not exist yet |
| DNS points elsewhere | **them** — it resolves, but not to gagarin |
| issuing certificate | gagarin — nothing for them to do |
| ok | nobody |

The generated address only ever reads `ok` or `issuing certificate`. It is never
waiting on the user, because there is nothing for them to do about it.

Things worth knowing before promising anything:

- **A deploy never changes an address.** `gg ship` and `gg deploy` cannot add
  one, change one or remove one. That is deliberate: forgetting a flag would take
  a live site down while the owner's DNS still looked correct.
- **Both addresses answer.** A service with a custom domain keeps its generated
  one, so existing links do not break and there is something to test with while
  DNS propagates.
- **A custom domain brings the generated one with it.** Claiming a name for a
  private service makes it public in the same call, so it is reachable somewhere
  from the moment the claim is made. Say out loud that you are putting it on the
  internet.
- **One domain, one service, across all of gagarin.** A name somebody else holds
  is refused; the refusal does not say who holds it.
- **An apex domain gets an A record, not a CNAME** — DNS does not permit a CNAME
  at an apex. The command prints the right one; do not "correct" it.

### Taking an address away

```
gg domain rm <project>/web shop.example.com    release a name they own
gg domain rm <project>/web                     make the service private again
```

**Both need a human's approval, every time**, the same emailed click that
`gg destroy` needs. This is destructive in the way deleting something is: what
breaks is invisible from the terminal and obvious to whoever was using the
address. Do not run either one unless the user asked for it in those words.

Releasing the generated address while a custom name is still declared is refused
— that would leave their DNS pointing at a host gagarin no longer serves. Release
the custom name first.

## Reading state

`gg projects` lists every project this account can reach — its own and those
shared with it — with the role held on each: `owner`, `editor` or `viewer`. Run
it before assuming a project exists or that you may deploy to it; a `viewer`
role means deploys will be refused, and that is worth knowing before the
attempt, not after. Names are unique only within one account, so two rows can
share a name — the id column is what tells them apart.

`gg status <project>` reports **desired state and actual cluster state side by side**, one
row per service, ending with what each one reaches. `●` is running what was
asked for, `◐` is still on its way there, `○` has stopped making progress — and
for the last two the cluster's own explanation is printed underneath. Trust
`gg status <project>` over your own memory of what you deployed — it reads the
cluster, not just the database.

Lines beginning `!` above the table are things to read **before** the table,
because they change what the table means. There are two:

- **"this project is suspended and nothing is running: …"** — every service
  shows `◌` and `0/0`, and deploys will be refused with `project_suspended`.
  The reason is in the line. If it says the account has run out of credit, the
  user can fix it at https://my.gagarin.cloud/billing and the services start
  again by themselves within a few minutes. Anything else is a decision gagarin
  made about this project, and only support can lift it — do not tell the user
  to add credit, because it will not help. **Nothing has been deleted**: the
  data, the addresses and the certificates are all still there.
- **"the reconciler last ran … ago"** — gagarin reporting on itself. The rows
  below are still an accurate reading of the cluster, but nothing is closing
  the gap between them and what was asked for any more. Tell the user; there is
  nothing an agent can do about it from here.

The READY column counts pods of **the revision you asked for**, which is why a
redeploy that will not start reads `0/1` even though the previous version is
still up and serving traffic. That is the honest number: the service is
answering, but not with what you shipped.

When someone is trying to understand how their services fit together, point
them at the project's page on https://my.gagarin.cloud — its right half is a
live dependency graph of exactly this. That is for the human, not for you:
read the plain output yourself.

Describe services to the user at the same altitude gagarin does: "web is a
public service on port 8080, reachable at <url>; worker is private." Give them
every address a service answers on, not just one — a service with a custom
domain has two, and picking one for them hides the one they might need. Do not
introduce Kubernetes vocabulary — namespaces, ingresses, pods and service
accounts are not part of this model and mentioning them is a regression.

## Sharing a project

A project has exactly one **owner** — the account that pays for it — plus any
number of **editors** and **viewers**.

```
gg members <project>                     who can reach it, and as what
gg share <project> <email>               add an editor (default)
gg share <project> <email> --role viewer
gg unshare <project> <email>             revoke access
```

An **editor** operates the project — deploy, delete individual services, manage
the roster — without paying for it. A **viewer** can read status, logs and the
member list, and nothing else. **destroying a project is the owner's alone**: deleting a
project takes its data and its URL with it, and only the account paying for that
can decide. Ownership itself is not in this list because it is the bill: it
cannot be granted, taken, or handed over here.

Two things follow that you should say out loud to the user rather than discover
for them:

- `gg share` with no `--role` grants **editor**, which can deploy over
  whatever is running. If the user asked for "read access" or "let them look at
  the logs", pass `--role viewer`.
- Sharing with somebody who has never used gagarin is allowed. The access waits
  for them; they get it when they sign up with that address. Nothing is emailed
  to them, so tell the user to let them know.

Ask before sharing. Access to a project is the user's to give, not yours to
infer from a name in the conversation.

## When something fails

Every error is JSON with a stable `code`, a `message`, and usually a `fix_hint`.
Act on the `code`, not the prose.

| code | what it means | what to do |
|---|---|---|
| `unauthorized` | this machine has no usable credential | run `gg whoami`, then the "Getting access" steps — never ask the user for a token |
| `insufficient_scope` | this credential does not carry that right — a browser session cannot deploy | deploy from the CLI, with the credential `gg auth` stored; the dashboard has no deploy button on purpose |
| `cannot_delegate` | a minted credential tried to mint another | mint it from the machine a human approved; `gg whoami` names the one you are holding |
| `name_required` | `gg creds create` with no `--name` | name it after where it will live — it is what you read in the list months later |
| `invalid_expiry` | `--expires` outside 1–365 days | there is no never; leave it out for 90 |
| `name_too_long` | a credential name over 120 characters | it appears in a list; shorten it |
| `approval_required` | a human must approve a deletion | tell the user, pass on the code, wait, retry the same command |
| `project_not_found` | no such project, **or** you have no access to it | `gg projects` lists what you can reach; `gg init <project>` creates one; if it is somebody else's, ask them to `gg share` it with you |
| `project_suspended` | the project is stopped and accepts no writes; reads still work | the message says why. Out of credit: the user adds some at https://my.gagarin.cloud/billing and it restarts by itself. Anything else: only support can lift it, so tell the user and stop — nobody next to you can fix this |
| `project_limit` | the account already has as many projects as it may hold | `gg destroy` one that is no longer needed, or the user writes to support@mail.gagarin.cloud for more |
| `insufficient_role` | you can see the project but only as a viewer | ask the owner or an editor for edit access; do not retry |
| `owner_only` | only the account that pays for the project may do this (deleting it) | tell the user to run it themselves; nobody can grant this, so do not retry |
| `invalid_role` | roles are `editor` and `viewer` | `owner` cannot be granted — it is the account that pays |
| `owner_not_a_member` | that address already owns the project | nothing to do; they have full access |
| `member_not_found` | that address has no access to remove | `gg members <project>` to see who does |
| `project_exists` | you already have a project with that name | deploy into it, or pick another name; names only have to be unique within your own account |
| `invalid_name` | not a usable name | lowercase letters, digits, hyphens; start with a letter; 2–30 chars |
| `image_required` | no image given | `gg push` one first, or use `gg ship` |
| `image_not_yours` | the image is not in this project's registry space | `gg build`/`gg push` into this project and deploy that; gg builds the path for you |
| `invalid_digest` | the digest is not a sha256 one | pass what `docker push` reported, or leave it out |
| `invalid_port` | port out of range | set the port the container actually listens on |
| `invalid_volume` | the volume path will not do | an absolute path inside the container, e.g. `/var/lib/postgresql/data` |
| `volume_immutable` | a volume is set once and never moves | keep the path and size it has, or destroy the service and deploy it again — which throws the data away |
| `invalid_domain` | not a hostname | a name the user owns, e.g. `shop.example.com`; no scheme, no port, no path |
| `domain_taken` | another service already holds that name | pick another, or release it wherever it is declared; the refusal does not say who has it |
| `domain_attached` | the service still has custom names declared | `gg domain rm <project>/<service> <name>` for each one first, or the user's DNS keeps pointing at a host that no longer answers |
| `invalid_needs` | a dependency list gg would not send: a blank name, or a service naming itself | correct the names; a service reaches itself without being told to |
| `invalid_deps` | the same, on the `--deps` of a deploy or a ship | correct the names; `gg deps ls` shows what is already declared |
| `not_a_service` | that name is a resource: you tried to deploy over it, roll it back or give it an address | a resource is reached by name from inside the project and is never public; `gg status <project>` shows which is which |
| `not_a_resource` | that name is a service, not a resource | `gg status <project>` shows which is which; use the service verbs |
| `no_such_resource` | this project has no resource by that name | `gg status <project>` lists what it has |
| `service_in_use` | something still declares that it needs this | `gg deps rm` the edge first; the refusal names what depends on it |
| `unknown_resource_type` | no such resource type | `postgres`, `ferretdb`, `valkey` or `external` |
| `wrong_resource_type` | a resource of another type already has that name | destroy it and create the one you meant — which throws its data away — or pick another name |
| `invalid_storage` | storage size out of range | 1 to 100 GB, set once, at creation |
| `no_storage` | this type has no volume to size | drop `--storage`; a valkey keeps everything in memory and an external runs nothing |
| `no_size` | an external has no container to size | drop `--size`; it is a set of values, not something gagarin runs |
| `invalid_env` | a key in `--env-file` will not do | every variable is published under the resource's own name, so write `API_KEY`, not `OPENAI_API_KEY` |
| `already_exists` | `gg resource add` with `--env` on an external that is already there | `gg resource rotate` is how values change — this refusal stops an old file rolling a key backwards |
| `env_required` | `gg resource rotate` on an external with no values | an external's values are the user's; pass `--env-file` |
| `no_env` | `--env` on a type that mints its own credentials | drop it; `gg resource secrets` reads what gagarin minted |
| `rotate_failed` | the running server could not be told its new password | **nothing changed** — the old credential still works and it is safe to retry; check `gg status` for a resource that is not running |
| `cannot_rotate` | the resource has no credentials recorded to replace | `gg resource secrets` shows what it holds; this is a bug worth reporting |
| `no_such_service` | a name in `gg deps add` or in `--deps` is not a service or resource in this project | `gg status <project>` lists them; create it first |
| `no_such_revision` | the service never had that revision | `gg history <project>/<service>` lists the ones it did |
| `nothing_to_roll_back_to` | the service has only ever been deployed once | nothing to do; this is not a bad call |
| `eject_failed` | the export would not assemble | a bug in gagarin, not in your call; report it |
| `apply_failed` | desired state saved, cluster update failed | retry the same command; it is idempotent |
| `cluster_error` | gagarin could not reach infrastructure | not your fault; report it to the user |
| `logs_unavailable` | no running pod yet | check `gg status <project>` first |
| `no_such_route` | wrong path **or** wrong method | you are calling the API directly and got the path wrong; use `gg`, which is the only supported client |
| `body_too_large` | request body over the limit | large values belong in a secret, not in a deploy call |
| `timeout` | the control plane gave up waiting on infrastructure | retry once; if it persists, report it to the user |
| `internal_error` | a bug in gagarin | report it; the control plane log has the detail |
| `rate_limited` | too many requests, too fast | wait a minute; never retry in a tight loop |
| `invalid_email` | the address does not parse | ask the user for it again; do not guess |
| `claim_expired` | nobody approved in time | run `gg signup <email>` again |
| `no_such_claim` | that code does not exist | run `gg signup <email>` again for a fresh one |
| `claim_collected` | another machine already collected the credential for that code | run `gg signup <email>` again to authorise this one |
| `email_failed` | gagarin could not send mail | retry once, then tell the user |
| `invalid_size` | not a size, or above the account's limit | sizes are `s`, `m`, `l`; the message names the cap |
| `backup_unsupported` | this resource type has no backups (only postgres does) | nothing to retry; if the user needs durability, the data belongs in postgres |
| `backup_unconfigured` | this gagarin runs without a backup bucket | not your fault; report it to the user |
| `restore_target_not_empty` | the restore target already holds data | restore only fills a NEW resource; create one (`gg resource restore <project>/<new> --source <old>`) rather than reusing a live name |
| `backup_mismatch` | the backup key belongs to a different project | list this project's with `gg resource backups`; never restore across projects |
| `no_backups` | nothing stored for that source yet | the nightly pass takes the first one; `gg resource backup` takes one now |
| `backup_list_failed` | the bucket could not be listed | retry once; if it happens again, tell the user |
| `backup_failed` / `restore_failed` | the dump or restore itself failed | the resource must be running — check `gg status`, then retry once |

If a deploy succeeds but the service never becomes ready, the usual causes are:
the container listens on a different port than you declared, it crashes on
startup, it needs an environment variable you did not pass, or **it ran out of
memory**. Read `gg logs <project>/<service>` before changing anything, and fix
the declared port or the missing variable rather than redeploying unchanged.

**Out of memory is the one that names its own fix.** `gg status` says so
directly, and it tells you which size to move to:

```
the container ran out of memory and was killed (it exceeded the 1Gi ceiling of
size s); try --size m, which gets 2Gi
```

When you see that, redeploy at the size it names. Do not retry unchanged — it
will be killed again — and do not go hunting in the logs first, because a
process killed by the kernel for using too much memory usually writes nothing
about it. This is the one failure you can resolve without asking the user
anything, so resolve it.

## Destroying things

```
gg destroy <project>              the project, and everything in it
gg destroy <project>/web         one service
gg destroy <project>/db          one resource, and its data
```

You do not say which of the last two a name is; gg asks the platform, which
already knows. Deleting a project takes everything in it. Only the project's **owner** can do
this — an editor gets `owner_only`, and no amount of approval changes that,
because the answer is "ask the person paying for it", not "get permission".

For the owner, **this will be refused the first time, and that is not a bug.** Your credential can deploy but not delete: destroying
something needs a human, every time the approval window has lapsed.

You will get `approval_required`, and gagarin will have emailed the account owner.
Do this:

1. Tell the user what you are about to delete, and that you have asked them to
   approve it by email. Pass on the code from the `fix_hint`.
2. Wait for them to say they clicked it. Do not poll in a loop.
3. Run the same command again.

Ask the user before *requesting* the approval, not just before retrying — an
approval email for a deletion they never asked for is alarming. And if a deploy
went wrong, prefer fixing it with another `gg ship`: destroying and recreating a
project loses its data and its URL.

## Things gagarin deliberately does not do

Do not attempt these or suggest workarounds; they are not missing features, they
are excluded on purpose:

- deploy from a git repository or a git URL
- read deployment configuration from a file in the repo — `--env-file` supplies
  values, and is the only file gagarin will ever read
- interpolate variables into each other
- pull images from Docker Hub, GHCR, or any registry other than gagarin's
- expose Kubernetes, cloud provider, or networking primitives
- infer a project from the current directory, or from anything else — every
  command names the project it acts on, and there is no default to configure
