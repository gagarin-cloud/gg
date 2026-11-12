# gg — the Gagarin CLI

`gg` deploys and operates applications on [Gagarin](https://gagarin.cloud), a
cloud your coding agent can drive and you can read.

It is a thin wrapper over the Gagarin API and holds no state of its own beyond a
credential file. Anything `gg` can do, the API can do — there is no second path,
and nothing about your deployment lives in your repository.

## Install

macOS and Linux:

```
curl -fsSL https://gagarin.cloud/install.sh | sh
```

Windows (PowerShell):

```
irm https://gagarin.cloud/install.ps1 | iex
```

The script downloads the release for your machine and checks it against the
release's `checksums.txt`. It refuses anything that does not match. It needs no
sudo or administrator rights: `gg` goes to `~/.local/bin`, or
`%LOCALAPPDATA%\Programs\gg` on Windows. `GAGARIN_INSTALL_DIR` changes that,
and `GAGARIN_INSTALL_VERSION=v0.54.0` pins a release. On macOS and Linux it
never edits your shell profile; if the directory is not on `PATH`, it prints
the line to add. Read it first if you like:
[install.sh](install.sh), [install.ps1](install.ps1).

### With a package manager

```
brew install --cask gagarin-cloud/tap/gg        # macOS
scoop bucket add gagarin https://github.com/gagarin-cloud/scoop-bucket
scoop install gagarin/gg                        # Windows
winget install Gagarin.gg                       # Windows
go install github.com/gagarin-cloud/gg@latest   # anywhere with Go
```

If you installed `gg` as a Homebrew formula, before casks, run `brew uninstall gg`
once first. Homebrew will not replace one with the other on its own.

Every [release](https://github.com/gagarin-cloud/gg/releases) also carries
`.deb`, `.rpm` and `.apk` packages for Linux, and archives for every platform:

```
sudo dpkg -i gg_<version>_linux_amd64.deb
sudo rpm -i  gg_<version>_linux_amd64.rpm
sudo apk add --allow-untrusted gg_<version>_linux_amd64.apk
```

`checksums.txt` is signed with [cosign](https://docs.sigstore.dev/), using the
identity of this repository's release workflow:

```
cosign verify-blob --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/gagarin-cloud/gg/.github/workflows/release.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com checksums.txt
sha256sum --ignore-missing -c checksums.txt
```

### Upgrade

```
gg upgrade
```

It upgrades the way you installed. Brew, scoop, winget and `go install` installs
are handed to that tool. A `gg` from the install script or an archive downloads
the new release and checks it against `checksums.txt` before replacing itself.
For a `.deb`, `.rpm` or `.apk`, which need root, it prints the command to run.
`gg upgrade --dry-run` says what it would do.

At a terminal, `gg` mentions a newer release on stderr, at most once a day.
`gg version` always checks. `GAGARIN_NO_UPDATE_CHECK=1` turns both off; the
notice is already off in CI.

### Uninstall

Delete the binary: `rm ~/.local/bin/gg`, or remove it with the package manager
that installed it. This machine's credential lives in `~/.config/gagarin/`.
Deleting that directory does not revoke it; `gg creds revoke` does.

## Teach your agent

`gg` drives the API; a separate **agent skill** teaches an agent how. It lives in
[gagarin-cloud/plugin](https://github.com/gagarin-cloud/plugin), not in this
binary.

In Claude Code, the plugin brings the skill, the MCP server, and the `setup`,
`ship`, `status` and `ci` skills at once:

```
/plugin marketplace add gagarin-cloud/plugin
/plugin install gagarin@gagarin-cloud
```

The same plugin is in the Plugins Directory that ChatGPT and Codex share
(search for Gagarin Cloud), or from the Codex CLI:

```
codex plugin marketplace add gagarin-cloud/plugin
```

For any other agent — Cursor, Copilot, Cline, Windsurf, Goose, opencode, Zed
and about seventy others:

```
npx skills add gagarin-cloud/plugin -g
```

`-g` installs for your user rather than the current repository; drop it to add
the skill to one project. `npx skills update` refreshes it later.

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
gg run shop/report report:v2 --schedule "0 3 * * *" --tz Europe/Berlin  # or run it on a cron schedule
gg domain add shop/web         # put it on the internet, at an address Gagarin gives you
gg status shop                 # what Gagarin intends, and what the cluster is actually doing
gg logs shop/web               # the latest 200 lines; a week is kept, crashed containers included
gg logs shop/web --since 2h --grep "panic:"
gg metrics shop                # CPU, memory, disk and traffic per service
gg metrics shop/web --window 7d  # one service over time: last, average, peak, against its limit

gg resource add shop/db postgres    # a database; you choose the name and the size, nothing else
gg resource add shop/db postgres --ext vector --ext postgis   # and the postgres extensions it has: the whole set
gg resource add shop/cache valkey   # postgres, qdrant, iggy, rabbitmq, valkey and s3 — valkey is in-memory and loses data on restart
gg deps add shop/web db             # let web reach it, and hand it DB_URL and the rest
gg resource add shop/jobs rabbitmq  # a message broker; durable queues and persistent messages survive restarts, and it is not backed up
gg resource add shop/files s3      # a private bucket; `gg resource public shop/files` opens it to the world for reading
gg resource secrets shop/db         # the values, when something outside the project needs them
gg connect shop/db                  # that database on 127.0.0.1, for as long as the command runs

gg resource add shop/openai external --env-file .env.openai   # a third-party key as a node on the graph
gg deps add shop/bot openai         # bot now holds OPENAI_API_KEY
gg resource rotate shop/openai --env-file .env.new   # new key, and every holder rolls
gg resource rotate shop/db          # a database: Gagarin mints it, no downtime

gg recipe snap shop --dry-run       # what publishing shop as a recipe would mint: version, floor, cost
gg recipe snap shop                 # publish it; prints its deploy link, my.gagarin.cloud/deploy/<id>
gg recipe show k3f9x2ma             # what a recipe runs, what it costs, the inputs it needs
gg recipe install k3f9x2ma myshop --input openai=.env.openai   # into a new project; secrets come from your file
gg recipe ls                        # the recipes you have published
gg recipe diff myshop               # what updating to its recipe's latest would change
gg recipe update myshop             # do it; a removal asks your human first
gg recipe rm shop@1.0.0             # withdraw a version, or with no @ the whole recipe (asks your human)

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

## Postgres extensions

A postgres's extensions are a set you restate, not a type you add. `--ext` on
`gg resource add` names one extension, spelled as `CREATE EXTENSION` spells it, and
is repeated for more, like `--deps` and `--env`; together they are every extension
the database should have. It works on a create and on a restatement alike:

```
gg resource add shop/db postgres --ext vector --ext postgis   # these two
gg resource add shop/db postgres --ext vector                 # drop postgis
gg resource add shop/db postgres --ext ""                     # none
gg resource add shop/db postgres --size m                     # no --ext: extensions left as they are
```

Adding one to a running postgres is immediate, with no restart. Dropping one is
immediate too, and asks nobody, because a drop refuses rather than loses anything.
It is never `CASCADE`, so a column, index or view that uses the extension — a
`vector` column, say — stops it; and it is refused while the extension's own
tables hold your data: pg_cron's jobs, pg_partman's partition config, a spatial
reference system you added to PostGIS. The answer is then `extensions_failed`
carrying postgres's own reason, and nothing in the restatement changed. A requirement comes with what needs it: `earthdistance` also
gives `cube`. Only a postgres takes `--ext` (anything else is `no_extensions`),
and `gg status` lists each one's extensions under its row.

| built in | |
|---|---|
| `vector` | pgvector: vector columns with HNSW and IVFFlat indexes, for embeddings |
| `postgis` | PostGIS: geometry and geography types, spatial indexes and functions (no raster) |
| `pg_cron` | scheduled SQL, run by the database itself (jobs run in database `app`) |
| `pg_partman` | partition management, created in schema `partman` |
| `pgaudit` | audit logging to the resource's log (set `pgaudit.log` to turn it on) |

Shipped with postgres: `pg_stat_statements`, `pg_trgm`, `pgcrypto`, `uuid-ossp`,
`citext`, `hstore`, `ltree`, `unaccent`, `fuzzystrmatch`, `btree_gin`,
`btree_gist`, `cube`, `earthdistance` (adds `cube`), `intarray`, `tablefunc`,
`amcheck`, `pg_buffercache`. Not offered: `timescaledb` (its licence forbids
exactly a hosted service), raster, `dblink`/`postgres_fdw`/`file_fdw`, and
untrusted languages. pgvector is `vector`, the name postgres knows it by.

`vector` on a postgres or a `qdrant`? `vector`, when an application already needs
postgres and adds retrieval: relational tables and embeddings in one database,
joined and in one transaction. `qdrant` when the vectors are the product.

## Building from source

```
go build -o gg .
go test ./...
```

No code generation, no build tags, no vendored tree.

## Licence

MIT. See [LICENSE](LICENSE).
