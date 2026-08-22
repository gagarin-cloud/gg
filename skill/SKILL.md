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
- A project owns **services**. A service is a container image that runs.
  - `public` services get an HTTPS URL automatically.
  - private services get no URL, but are reachable from other services in the
    same project by name: `http://worker:8080`.
- A project owns **resources** (databases and similar). Not yet available.
- Gagarin runs images **only from its own registry**. Pushing an image never
  deploys it — those are separate steps on purpose. To run something you did not
  build — postgres, redis, anything public — copy it in first:
  `gg registry copy postgres:17-alpine`. Do not write a one-line Dockerfile that
  only says `FROM`; that is the same thing, worse.

Gagarin itself holds the source of truth for all of this. Do not try to write
config into the repository; it will not be read.

## Installing gg

Run `gg whoami`. If the command is found — even if it reports no credentials —
`gg` is installed and you can skip to the next section.

If the shell reports that `gg` does not exist, install it:

```
go install github.com/vikar-ltd/gg@latest
```

That needs Go. If the machine has none, take a released binary for its platform
from https://github.com/vikar-ltd/gg/releases — every release publishes
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

If it says the machine has no credentials:

1. **Ask the user for their email address.** Do not guess it, and do not use an
   address you found in the repository or in git history — a deploy that lands in
   a stranger's account is worse than no deploy.
2. `gg signup <email>`
3. **Tell the user to click the button in the email.** That single click creates
   the account and authorises this machine. Say the code `gg signup` printed, so
   they can check the email is the one you triggered.
4. `gg auth --claim <code>` — this waits for the click, then stores credentials in
   `~/.config/gagarin/credentials.json`.

The credential this produces can deploy, read status, and read logs. It **cannot
delete anything**: see "Destroying things" below.

You never handle the credential yourself, and you should not read that file or
echo it anywhere. If a command needs authorisation it will say so.

Then `gg registry login` once per machine, so `docker` can push. It uses the
credential this machine already holds — there is no second account, and nothing
to type. If it reports that the credential is not valid, run `gg auth`.

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

3. **Deploy each service.** Deploy private services *before* the public ones
   that call them — `-needs` can only name a service that already exists:
   ```
   gg deploy -project <project> -name worker -port 8080 -private
   gg deploy -project <project> -name web -port 8080 -needs worker -env WORKER_URL=http://worker:8080
   ```
   `-needs` is not documentation: without it `web` cannot reach `worker` at all.
   See "Wiring services together" below — including why a missing `-needs` hangs
   instead of failing.
   `gg deploy` builds the image in the current directory, pushes it, registers
   the service, and prints the URL. Run it from the directory containing that
   service's Dockerfile.

4. **Verify** and give the user the URL:
   ```
   gg status <project>
   ```

## Environment variables

Set them individually, or read them from a plain `KEY=VALUE` file:

```
gg deploy -project <project> -name web -port 8080 -env-file .env
gg deploy -project <project> -name web -port 8080 -env-file .env -env DEBUG=false
```

- `-env-file` is **not** picked up automatically. If the user has a `.env` and
  wants it used, pass the flag; never assume a file should be read.
- `-env` flags override any file. Later `-env-file` flags override earlier ones.
- There is **no interpolation**: `B=${A}` sets `B` to the literal text `${A}`.
- Env is part of a service's desired state and is **replaced** on each deploy, not
  merged. To remove a variable, deploy again without it. To keep a variable, keep
  passing it.

The file supplies **values only**. It cannot name services, set ports, or say
what is public — that is not a limitation to work around, it is the design. If
you find yourself wanting to put deployment structure in a file, stop: gagarin
holds that itself and the only way to set it is the flags above.

## Wiring services together

A private service is reachable at `http://<service-name>:<port>` from inside the
same project — but **only by the services that declared they call it.**

Declare that with `-needs`, on the service doing the calling:

```
gg deploy -name api -needs db -env DATABASE_URL=postgres://user:pass@db:5432/app
```

`db` now accepts connections from `api` and from nothing else. Deploy `api`
without `-needs db` and the address still resolves, the connection is simply
never answered.

**This is the part that will waste your time if you skip it.** A call nobody
declared is not refused — it is dropped. So it does not fail fast with
"connection refused"; it **hangs until the client's timeout**, which for a
database driver can be 30 seconds or forever. A hang is the symptom of a missing
`-needs` far more often than it is a bug in the application.

So when a service hangs talking to another one, check `gg status` *first*. It
ends every service's line with what that service can reach:

```
api is a private service on port 4000, reaching db.
web is a public service on port 8080, reachable at https://…, reaching nothing else.
```

`reaching nothing else` is a statement of fact, not a warning — most services
legitimately need nothing. But if you expected a name there and it is missing,
that is your hang, and the fix is **a redeploy** of the caller with the
`-needs` it was missing. There is no separate command to connect two services
after the fact, because the deploy is the only place deployment structure is set.

**`-needs` is replaced wholesale on every deploy, exactly like `-env`.** If `api`
calls both `db` and `cache`, every future deploy of `api` must pass both:

```
gg deploy -name api -needs db -needs cache
```

Pass only `-needs db` next time and `api` stops being able to reach `cache` —
same trap as forgetting an environment variable, same fix. Read the current set
out of `gg status` before redeploying something you did not deploy yourself.

Two consequences worth knowing:

- **The direction matters.** `-needs` goes on the *caller*. If `api` queries `db`,
  it is `api` that is deployed with `-needs db`, never the other way round.
- **Public URLs are not covered by this.** If one service calls another's public
  `https://` address, that arrives the same way a stranger's browser does and is
  allowed. The guarantee is about private, in-project addresses: *a private
  service is unreachable unless something declared it needs it.*

To take a service away entirely, `gg destroy -service <name>`. It is refused
while anything still needs it, and names what does — redeploy that service
without the `-needs` first.

## Reading state

`gg projects` lists every project this account can reach — its own and those
shared with it — with the role held on each: `owner`, `editor` or `viewer`. Run
it before assuming a project exists or that you may deploy to it; a `viewer`
role means deploys will be refused, and that is worth knowing before the
attempt, not after. Names are unique only within one account, so two rows can
share a name — the id column is what tells them apart.

`gg status` reports **desired state and actual cluster state side by side**. A
service marked `*` agrees; one marked `!` does not. Trust `gg status` over your
own memory of what you deployed — it reads the cluster, not just the database.

Describe services to the user at the same altitude gagarin does: "web is a
public service on port 8080, reachable at <url>; worker is private." Do not
introduce Kubernetes vocabulary — namespaces, ingresses, pods and service
accounts are not part of this model and mentioning them is a regression.

## Sharing a project

A project has exactly one **owner** — the account that pays for it — plus any
number of **editors** and **viewers**.

```
gg members <project>              who can reach it, and as what
gg share <email> <project>        add an editor (default)
gg share <email> <project> -role viewer
gg unshare <email> <project>      revoke access
```

An **editor** operates the project — deploy, delete individual services, manage
the roster — without paying for it. A **viewer** can read status, logs and the
member list, and nothing else. **`gg destroy` is the owner's alone**: deleting a
project takes its data and its URL with it, and only the account paying for that
can decide. Ownership itself is not in this list because it is the bill: it
cannot be granted, taken, or handed over here.

Two things follow that you should say out loud to the user rather than discover
for them:

- `gg share <email>` with no `-role` grants **editor**, which can deploy over
  whatever is running. If the user asked for "read access" or "let them look at
  the logs", pass `-role viewer`.
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
| `approval_required` | a human must approve a deletion | tell the user, pass on the code, wait, retry the same command |
| `project_not_found` | no such project, **or** you have no access to it | `gg init <project>` first; if it is somebody else's, ask them to `gg share` it with you |
| `insufficient_role` | you can see the project but only as a viewer | ask the owner or an editor for edit access; do not retry |
| `owner_only` | only the account that pays for the project may do this (deleting it) | tell the user to run it themselves; nobody can grant this, so do not retry |
| `invalid_role` | roles are `editor` and `viewer` | `owner` cannot be granted — it is the account that pays |
| `owner_not_a_member` | that address already owns the project | nothing to do; they have full access |
| `member_not_found` | that address has no access to remove | `gg members <project>` to see who does |
| `project_exists` | you already have a project with that name | deploy into it, or pick another name; names only have to be unique within your own account |
| `invalid_name` | not a usable name | lowercase letters, digits, hyphens; start with a letter; 2–30 chars |
| `image_required` | no image given | push an image first |
| `image_not_yours` | the image is not in this project's registry space | push to this project and deploy that; `gg deploy` handles the path for you |
| `invalid_digest` | the digest is not a sha256 one | pass what `docker push` reported, or leave it out |
| `invalid_port` | port out of range | set the port the container actually listens on |
| `apply_failed` | desired state saved, cluster update failed | retry the same command; it is idempotent |
| `cluster_error` | gagarin could not reach infrastructure | not your fault; report it to the user |
| `logs_unavailable` | no running pod yet | check `gg status` first |
| `no_such_route` | wrong path **or** wrong method | re-read the endpoint list above; do not invent endpoints |
| `body_too_large` | request body over the limit | large values belong in a secret, not in a deploy call |
| `timeout` | the control plane gave up waiting on infrastructure | retry once; if it persists, report it to the user |
| `internal_error` | a bug in gagarin | report it; the control plane log has the detail |
| `rate_limited` | too many requests, too fast | wait a minute; never retry in a tight loop |
| `invalid_email` | the address does not parse | ask the user for it again; do not guess |
| `claim_expired` | nobody approved in time | run `gg signup <email>` again |
| `email_failed` | gagarin could not send mail | retry once, then tell the user |

If a deploy succeeds but the service never becomes ready, the usual causes are:
the container listens on a different port than you declared, it crashes on
startup, or it needs an environment variable you did not pass. Read `gg logs
<service>` before changing anything, and fix the declared port or the missing
variable rather than redeploying unchanged.

## Destroying things

```
gg destroy <project>
```

Deletes the project and everything in it. Only the project's **owner** can do
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
went wrong, prefer fixing it with another `gg deploy`: destroying and recreating a
project loses its data and its URL.

## Things gagarin deliberately does not do

Do not attempt these or suggest workarounds; they are not missing features, they
are excluded on purpose:

- deploy from a git repository or a git URL
- read deployment configuration from a file in the repo — `-env-file` supplies
  values, and is the only file gagarin will ever read
- interpolate variables into each other
- pull images from Docker Hub, GHCR, or any registry other than gagarin's
- expose Kubernetes, cloud provider, or networking primitives
