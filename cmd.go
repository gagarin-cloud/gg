package main

// The command tree.
//
// This file only wires flags and positional arguments to the business logic
// that lives next to each concern (auth.go, image.go, deps.go, registry.go,
// rollback.go, ...). Cobra owns dispatch, --help generation and flag parsing;
// nothing here should contain a decision the API or the functions it calls
// could make instead.
//
// One rule runs through all of it: nothing is derived from where the command
// was run. Every project-scoped command names its project, in the argument, as
// `project` or `project/name`. See ref.go for why.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// usageArgs validates a positional-argument count and, on failure, reports
// the usage line rather than cobra's generic "accepts N arg(s)" — the same
// voice the rest of this CLI's errors use.
func usageArgs(min, max int, usage string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < min || len(args) > max {
			return errors.New(usage)
		}
		return nil
	}
}

// atLeastArgs is the same for the commands that take a list — `gg deps add`
// takes one service and any number of names.
func atLeastArgs(min int, usage string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < min {
			return errors.New(usage)
		}
		return nil
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "gg",
		Short: "The gagarin CLI",
		Long: `gg is a thin wrapper over the gagarin API. It holds no state of its own
beyond an access token: anything gg can do, the API can do — there is no
second path.

Everything lives in a project, and everything is named for it:

  gg status shop                        a project
  gg logs shop/web                      a service in one
  gg deploy shop/web:8080 api:v3        that service, on that port, running
                                        that image

Nothing is inferred from the directory you are standing in.

Credentials live in ~/.config/gagarin/credentials.json after "gg auth".
There is nothing to export.

Environment (overrides the file; meant for CI):
  GAGARIN_API      control plane URL (default ` + defaultAPI + `)
  GAGARIN_TOKEN    a credential, for CI where no human can click a link
  GAGARIN_REGISTRY registry host, e.g. registry.gagarin.cloud`,
		SilenceErrors: true,
		SilenceUsage:  true,
		Version:       versionString(),
	}
	root.SetVersionTemplate("{{.Version}}\n")

	root.AddCommand(
		newSignupCmd(),
		newAuthCmd(),
		newWhoamiCmd(),
		newInitCmd(),
		newProjectsCmd(),
		newBuildCmd(),
		newPushCmd(),
		newDeployCmd(),
		newShipCmd(),
		newStatusCmd(),
		newLogsCmd(),
		newDepsCmd(),
		newShareCmd(),
		newUnshareCmd(),
		newMembersCmd(),
		newDestroyCmd(),
		newHistoryCmd(),
		newRollbackCmd(),
		newEjectCmd(),
		newResourceCmd(),
		newDomainCmd(),
		newRegistryCmd(),
		newSkillCmd(),
		newVersionCmd(),
	)
	return root
}

func newSignupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "signup EMAIL",
		Short: "ask for an account; a human approves by email",
		Args: usageArgs(1, 1, "usage: gg signup EMAIL\n"+
			"  ask your human for their address — do not guess it"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdSignup(args[0])
		},
	}
}

func newAuthCmd() *cobra.Command {
	var claim string
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "wait for that approval and store credentials",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Accept a bare code too: an agent that read the instructions may well
			// try `gg auth ABCD-1234`, and refusing on syntax would be pedantry.
			if claim == "" && len(args) > 0 {
				claim = args[0]
			}
			return cmdAuth(claim)
		},
	}
	cmd.Flags().StringVar(&claim, "claim", "", "the code gg signup printed")
	return cmd
}

func newWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "which account this machine acts as",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdWhoami()
		},
	}
}

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init PROJECT",
		Short: "create a project",
		Args: usageArgs(1, 1, "usage: gg init PROJECT\n"+
			"  lowercase letters, digits and hyphens, e.g. gg init shop"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdInit(args[0])
		},
	}
}

func newProjectsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "projects",
		Short: "every project you can reach, and your role on it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdProjects()
		},
	}
}

// --- images ----------------------------------------------------------------

// bindBuildFlags is shared by `gg build` and `gg ship`, which build the same way
// and must not disagree about how.
func bindBuildFlags(cmd *cobra.Command, f *buildFlags) {
	cmd.Flags().StringVar(&f.context, "context", ".",
		"directory to build from — the one the Dockerfile can COPY out of")
	cmd.Flags().StringVar(&f.file, "file", "",
		"the Dockerfile (default: Dockerfile in the context directory)")
}

func newBuildCmd() *cobra.Command {
	var f buildFlags
	cmd := &cobra.Command{
		Use:   "build PROJECT/IMAGE[:TAG]",
		Short: "build an image into a project's registry space",
		Long: `Build a container image and name it in a project's space in the gagarin
registry.

The tag is optional: without one gg mints it from the clock, because most
of the time nobody cares what a particular build is called. Building does
not run anything — "gg push" uploads it, "gg deploy" releases it, and
"gg ship" is all three.

The platform is not yours to choose. gagarin reports what its own nodes
run and the build targets that, because an image built for the wrong
architecture succeeds here and fails much later at pull time.`,
		Args: usageArgs(1, 1, "usage: gg build PROJECT/IMAGE[:TAG]\n"+
			"  e.g. gg build shop/api:v3 --context ./api"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdBuild(args[0], f)
		},
	}
	bindBuildFlags(cmd, &f)
	cmd.Flags().BoolVar(&f.push, "push", false, "upload it afterwards, as \"gg push\" would")
	return cmd
}

func newPushCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "push PROJECT/IMAGE:TAG",
		Short: "upload an image you have built",
		Long: `Upload an image to the gagarin registry.

The tag is required here, unlike on "gg build": a push moves something
that already exists, and "whichever one I built last" is not a name.

Pushing never deploys. That is the point of it being its own verb — CI
can publish an image on every commit and release it on some of them.`,
		Args: usageArgs(1, 1, "usage: gg push PROJECT/IMAGE:TAG\n"+
			"  e.g. gg push shop/api:v3"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdPush(args[0])
		},
	}
}

func newDeployCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy PROJECT/SERVICE[:PORT] IMAGE[:TAG]",
		Short: "run an image that is already in the registry",
		Long: `Run an image as a named service.

  gg deploy shop/web:8080 api:v3

The port is the one the container listens on, and defaults to 8080. The
image is one in this project's own space — gagarin runs nothing from
anywhere else, so bring somebody else's in with "gg registry copy" first.

A new service is private: reachable inside its own project, by name, by
the services that have declared they need it — and from nowhere else.
"gg domain add" is what puts one on the internet. Exposure is something
you say out loud, because the two mistakes do not cost the same: a service
that should have been public fails the first time somebody opens it, and a
service that should have been private does not fail at all.

A deploy changes the image and the environment. It cannot give a service
an address or take one away, move a volume, or withdraw anything this
service is allowed to reach: those are declared by "gg domain", by the
first deploy, and by "gg deps", and none of them can be released by a
deploy that forgets to mention them.

  gg deploy shop/api:8080 api:v3 --deps db

--deps is the one thing here that touches the graph, and it only ever
adds. It is for the service that needs a database on its very first
deploy: declaring it in the same call means the pod never starts into a
window where it cannot reach the database and does not hold its
password. Forgetting it on the next deploy changes nothing, which is why
it is safe where the old --needs flag was not.

Environment is replaced wholesale, because it is part of what this
revision ran with and is what a rollback puts back. Pass every variable
the service needs, every time.

What the service holds is that environment plus the connection variables
of any resource it reaches. Those are not passed here and are not stored
against the revision — the platform derives them from the graph every
time it starts the pod, so they cannot go stale and a redeploy cannot
forget them. A rollback restores the image and the environment you
deployed with, never an old password.`,
		Args: usageArgs(2, 2, "usage: gg deploy PROJECT/SERVICE[:PORT] IMAGE[:TAG]\n"+
			"  e.g. gg deploy shop/web:8080 api:v3\n"+
			"  build one first with gg build, or gg ship to do both"),
	}
	v := bindDeployFlags(cmd.Flags())
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		f, err := v.finish()
		if err != nil {
			return err
		}
		return cmdDeploy(args[0], args[1], f)
	}
	return cmd
}

func newShipCmd() *cobra.Command {
	var b buildFlags
	cmd := &cobra.Command{
		Use:   "ship PROJECT/SERVICE[:PORT]",
		Short: "build, push and deploy in one go",
		Long: `Build the current directory, upload it, and run it as a service.

  gg ship shop/web:8080

This is the everyday path, and the three commands underneath it — build,
push, deploy — are there for when you want the steps apart: publishing
without releasing, releasing an image you already have, or rolling
forward to a tag somebody else built.

The image is named after the service and tagged from the clock, since
somebody shipping does not have a name in mind for this particular build.
It is printed, so it can be deployed again later.

As with "gg deploy", this changes the image and the environment — and
takes the same --deps, which adds to what the service may reach and
hands it the connection variables of any resource among them:

  gg ship shop/api:8080 --deps db

A new service is private until "gg domain add" gives it an address, and
one that already has an address keeps it.`,
		Args: usageArgs(1, 1, "usage: gg ship PROJECT/SERVICE[:PORT]\n"+
			"  e.g. gg ship shop/web:8080 --context ./web"),
	}
	bindBuildFlags(cmd, &b)
	v := bindDeployFlags(cmd.Flags())
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		d, err := v.finish()
		if err != nil {
			return err
		}
		return cmdShip(args[0], b, d)
	}
	return cmd
}

// --- reading state ---------------------------------------------------------

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status PROJECT",
		Short: "desired vs actual state for every service",
		Args: usageArgs(1, 1, "usage: gg status PROJECT\n"+
			"  gg projects lists the ones you can reach"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdStatus(args[0])
		},
	}
	return cmd
}

func newLogsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logs PROJECT/SERVICE",
		Short: "recent logs",
		Args:  usageArgs(1, 1, "usage: gg logs PROJECT/SERVICE\n  e.g. gg logs shop/web"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdLogs(args[0])
		},
	}
}

// --- the dependency graph --------------------------------------------------

// newDepsCmd is its own verb rather than a flag on deploy, for the reason
// spelled out at the top of deps.go: a network path that can be withdrawn by
// forgetting to restate it fails silently, because an undeclared call hangs
// rather than being refused.
func newDepsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deps",
		Short: "which services may reach which, inside a project",
		Long: `Every service in a project is default-denied.

A private service is reachable only from services that have declared they
need it, and an undeclared call is dropped rather than refused — so it does
not fail fast, it hangs until the client gives up.

  gg deps add shop/api db cache     api may now reach db and cache
  gg deps ls  shop/api              what it reaches today
  gg deps rm  shop/api cache        and no longer cache

The direction matters. The declaration goes on the caller: if api queries
db, it is api that needs db, never the other way round.

When the thing reached is a resource, this also hands the caller that
resource's connection variables — "gg deps add shop/api db" gives api
DB_URL, DB_HOST, DB_PORT, DB_USER, DB_PASSWORD and DB_DATABASE, and the
command prints which ones arrived. They are named after the resource
rather than the protocol, so a service can reach two databases without
their variables colliding. "gg resource secrets" prints the values.

So connecting is this one call. It used to be two — read the credentials,
pass them to a deploy — and that is no longer necessary.

A deploy cannot withdraw any of this, and "gg deploy --deps" can only add
to it. Withdrawing is "gg deps rm", here, and nowhere else.`,
	}
	cmd.AddCommand(newDepsListCmd(), newDepsAddCmd(), newDepsRemoveCmd())
	return cmd
}

func newDepsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ls PROJECT/SERVICE",
		Aliases: []string{"list"},
		Short:   "what this service is allowed to reach",
		Args:    usageArgs(1, 1, "usage: gg deps ls PROJECT/SERVICE\n  e.g. gg deps ls shop/api"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdDepsList(args[0])
		},
	}
}

func newDepsAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add PROJECT/SERVICE NAME...",
		Short: "let it reach these as well, and hold their credentials",
		Args: atLeastArgs(2, "usage: gg deps add PROJECT/SERVICE NAME...\n"+
			"  e.g. gg deps add shop/api db cache\n"+
			"  the names are services in the same project; there is no reaching across one"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdDepsAdd(args[0], args[1:])
		},
	}
}

func newDepsRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "rm PROJECT/SERVICE NAME...",
		Aliases: []string{"remove"},
		Short:   "stop it reaching these, and take their credentials away",
		Args: atLeastArgs(2, "usage: gg deps rm PROJECT/SERVICE NAME...\n"+
			"  e.g. gg deps rm shop/api cache"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdDepsRemove(args[0], args[1:])
		},
	}
}

// --- sharing ---------------------------------------------------------------

func newShareCmd() *cobra.Command {
	var role string
	cmd := &cobra.Command{
		Use:   "share PROJECT EMAIL",
		Short: "give somebody access to a project",
		Args: usageArgs(2, 2, "usage: gg share PROJECT EMAIL [--role editor|viewer]\n"+
			"  e.g. gg share shop teammate@example.com"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdShare(args[0], args[1], role)
		},
	}
	cmd.Flags().StringVar(&role, "role", "editor",
		"editor deploys and manages but cannot delete the project; viewer reads")
	return cmd
}

func newUnshareCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unshare PROJECT EMAIL",
		Short: "take that access away",
		Args:  usageArgs(2, 2, "usage: gg unshare PROJECT EMAIL"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdUnshare(args[0], args[1])
		},
	}
}

func newMembersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "members PROJECT",
		Short: "who can reach a project, and as what",
		Args:  usageArgs(1, 1, "usage: gg members PROJECT"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdMembers(args[0])
		},
	}
}

// --- destroying ------------------------------------------------------------

func newDestroyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "destroy PROJECT | PROJECT/NAME",
		Short: "delete a project, or one thing inside it",
		Long: `Delete something. Which thing depends only on what you name:

  gg destroy shop        the project, and everything in it
  gg destroy shop/web    one service
  gg destroy shop/db     one resource, and its data

You do not have to say which of the last two a name is — gg asks the
platform, because it already knows.

Destroying anything needs a human's approval, every time the approval
window has lapsed, and only a project's owner can destroy the project.`,
		Args: usageArgs(1, 1, "usage: gg destroy PROJECT | PROJECT/NAME\n"+
			"  gg destroy shop        the whole project\n"+
			"  gg destroy shop/web    one service or resource in it"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdDestroy(args[0])
		},
	}
}

// --- history ---------------------------------------------------------------

func newHistoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "history PROJECT/SERVICE",
		Short: "every deploy of a service, newest first",
		Args:  usageArgs(1, 1, "usage: gg history PROJECT/SERVICE\n  e.g. gg history shop/web"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdHistory(args[0])
		},
	}
}

func newRollbackCmd() *cobra.Command {
	var to int
	cmd := &cobra.Command{
		Use:   "rollback PROJECT/SERVICE",
		Short: "put the previous deploy back",
		Long: `Restore a recorded revision by deploying it again.

It restores the image and the environment that revision ran with. It does
not restore what the service was allowed to reach, or its domain, or its
volume — those are declarations about the shape of the project rather than
parts of the artifact, and putting yesterday's code back says nothing
about them.

A rollback is itself a deploy: it is recorded as a new revision naming the
one it restored, and nothing leaves the history.`,
		Args: usageArgs(1, 1, "usage: gg rollback PROJECT/SERVICE [--to REVISION]\n"+
			"  e.g. gg rollback shop/web"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdRollback(args[0], to)
		},
	}
	cmd.Flags().IntVar(&to, "to", 0,
		"a particular revision instead (see gg history). Refused across a\nchange of volume")
	return cmd
}

func newEjectCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "eject PROJECT",
		Short: "the Kubernetes manifests for this project. Owner only",
		Long: "the Kubernetes manifests for this project, so you can run it\n" +
			"somewhere else. Owner only.",
		Args: usageArgs(1, 1, "usage: gg eject PROJECT [-o FILE]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdEject(args[0], out)
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "write to a file instead of stdout")
	return cmd
}

// --- resources -------------------------------------------------------------

func newResourceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resource",
		Short: "databases and the like: what a project has, rather than what it runs",
	}
	cmd.AddCommand(newResourceAddCmd(), newResourceSecretsCmd(), newResourceRotateCmd(),
		newResourceBackupsCmd(), newResourceBackupCmd(), newResourceRestoreCmd())
	return cmd
}

func newResourceAddCmd() *cobra.Command {
	var storage, size = 0, ""
	var v *envFlagVars
	cmd := &cobra.Command{
		Use:   "add PROJECT/NAME TYPE",
		Short: "provision a resource, e.g. `gg resource add shop/db postgres`",
		Long: `Provision something gagarin runs for you, rather than something you built.

  postgres   PostgreSQL 17, on a volume. Survives restarts.
  ferretdb   A MongoDB-compatible document database, on a volume.
             Survives restarts. Your mongodb:// URL, driver and ODM work
             unchanged; what runs is FerretDB, the Apache-licensed
             implementation, storing into Postgres.
  valkey     An in-memory store speaking the redis protocol — every
             redis client and every redis:// URL work unchanged.
             --storage is refused, and a restart loses everything in
             it: this is a cache, not a database.
  external   Something gagarin does NOT run: an OpenAI account, a Stripe
             key, a bucket elsewhere. It holds the values you give it and
             publishes them to whatever declares it needs them. No
             container, so --size and --storage are refused.

You name it, say how big its storage may get, and pick a size. Every
other decision is the platform's.

  --size s   0.5 vCPU / 1 GB, shared. Fine for development.
  --size m   1 vCPU / 2 GB, dedicated. What a real database wants.
  --size l   2 vCPU / 4 GB, dedicated.

One instance, one volume, no failover. Postgres is dumped nightly and
kept fourteen days — ` + "`gg resource backups`" + ` lists them, and
` + "`gg resource restore`" + ` puts one back into a NEW resource. Valkey keeps
nothing across a restart, by design; ferretdb has no backups yet. See
` + "`gg deps add`" + ` for how to connect something to it — that one call opens
the route and hands over the credentials — and the docs for what all this
means before you put a client's data in one.

An external is the exception to most of the above. It runs nothing, so
there is no size, no storage, no backup and nothing to be ready. You give
it the values instead, and it publishes them under its own name:

  gg resource add shop/openai external --env-file .env.openai

Restating a resource cannot change what it publishes — that is
` + "`gg resource rotate`" + `, deliberately a separate verb so that restating
one from an old file cannot roll a key backwards by accident.

An .env.openai holding API_KEY and BASE_URL makes shop/openai publish
OPENAI_API_KEY and OPENAI_BASE_URL. Name the keys without the prefix —
API_KEY, not OPENAI_API_KEY, which is refused rather than doubled.

Prefer --env-file to --env. A key on the command line goes into your
shell history and into the transcript of every agent that ran the
command; a file does not.

An external's values are set here, once, when it is created. Changing
them afterwards is ` + "`gg resource rotate`" + `.

Declaring a dependency on an external does NOT restrict egress. Anything
in a project can already reach the internet; what the declaration grants
is the credentials and a line on the graph saying who uses them.`,
		Args: usageArgs(2, 2, "usage: gg resource add PROJECT/NAME TYPE\n"+
			"  e.g. gg resource add shop/db postgres\n"+
			"  the types that exist are: postgres, ferretdb, valkey, external"),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := v.finish()
			if err != nil {
				return err
			}
			return cmdResourceAdd(args[0], args[1], size, storage, e)
		},
	}
	// The only knob, and it is honoured rather than negotiated. Anything else
	// about how a resource runs is the platform's decision.
	cmd.Flags().IntVar(&storage, "storage", 0,
		"how big its storage may get, in GB (default 10).\nCan be raised later by restating the resource with a\nbigger number; it can never be made smaller.\nRefused for valkey, which keeps nothing across a restart")
	// Unlike --storage, this one can be changed afterwards: a size is cheap and
	// reversible where a volume is neither.
	cmd.Flags().StringVar(&size, "size", "",
		"how much CPU and memory: s, m or l (default s).\nCan be changed later by restating the resource")
	// Only an external takes these, and cmdResourceAdd refuses them for every
	// other type before it makes a request. Registered unconditionally because a
	// flag that exists for one value of a positional argument cannot be hidden
	// per-invocation — and a refusal naming the type is a better answer than
	// "unknown flag" anyway.
	//
	// --env-file first in the help, because it is the one to use: a key passed
	// on the command line is in the shell history and in every agent transcript
	// that ran it.
	v = bindEnvFlags(cmd.Flags(),
		"a value an external publishes, K=V (repeatable).\nPrefer --env-file: this goes into your shell history",
		"read an external's values from KEY=VALUE lines\n(repeatable; later files win, --env wins over all files)")
	return cmd
}

func newResourceSecretsCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "secrets PROJECT/NAME",
		Short: "print its credentials, for a human or a client outside gagarin",
		Long: `Print what a caller needs to connect, and nothing else.

You do not need this to connect a service in the same project. That is
one call, and it grants the variables as well as the route:

  gg deps add shop/api db

This command is for the cases where something else needs the values: a
psql on your laptop, checking what an application is being handed, or a
client running outside gagarin. It prints the same variables the platform
injects — named after the resource, so a postgres called db gives DB_URL,
DB_HOST, DB_PORT, DB_USER, DB_PASSWORD and DB_DATABASE.

Treat the output as a credential. It is a live password, and --format env
exists to be piped, not pasted into a terminal somebody is sharing.`,
		Args: usageArgs(1, 1, "usage: gg resource secrets PROJECT/NAME\n  e.g. gg resource secrets shop/db"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdResourceSecrets(args[0], format)
		},
	}
	cmd.Flags().StringVar(&format, "format", "env",
		"env (KEY=VALUE lines, for --env-file) or json")
	return cmd
}

// newResourceRotateCmd is its own verb rather than a flag or a restatement,
// and that separation is the fix for a footgun rather than a preference.
//
// An external's values used to be replaceable by restating the resource, which
// meant restating it from a stale .env file silently rotated the key backwards —
// and the failure was an authentication error nobody would connect to the
// command they ran. A declaration says what something should be; replacing a
// live credential is an act with a moment, and the two want different verbs.
func newResourceRotateCmd() *cobra.Command {
	var v *envFlagVars
	cmd := &cobra.Command{
		Use:   "rotate PROJECT/NAME",
		Short: "replace its credentials, and roll everything holding them",
		Long: `Give a resource new credentials. Everything that declares it needs the
resource is restarted with them.

  gg resource rotate shop/db                                 a database
  gg resource rotate shop/openai --env-file .env.openai.new  an external

Who supplies the new value is the only difference between the types. For
a postgres, ferretdb or valkey, gagarin mints one and --env is refused —
a password you chose is one the running server has never heard of. For an
external the values are yours, so --env or --env-file is required.

Nothing is printed but the names of the variables. Read the values with
"gg resource secrets" if you need them.

What happens per type, because the costs are not the same:

  postgres   The running server is told immediately. No restart, no
             downtime, no dropped connections beyond the ones that were
             mid-authentication.
  ferretdb   The same, in the Postgres it stores into.
  valkey     The password is read when the server starts, so the pod is
             replaced — which empties the cache. That is what a restart
             of a valkey always does, but it is worth knowing before you
             run this on a hot one.
  external   Nothing of ours runs, so nothing of ours restarts. Only the
             services holding the values are rolled.

If it fails, nothing changed: the old credential is still in use and the
command is safe to run again.`,
		Args: usageArgs(1, 1, "usage: gg resource rotate PROJECT/NAME\n"+
			"  e.g. gg resource rotate shop/db\n"+
			"  for an external: gg resource rotate shop/openai --env-file .env.new"),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := v.finish()
			if err != nil {
				return err
			}
			return cmdResourceRotate(args[0], e)
		},
	}
	// Same pair as `gg resource add`, and --env-file for the same reason: a key
	// on the command line is in the shell history and in every agent transcript
	// that ran it.
	v = bindEnvFlags(cmd.Flags(),
		"a new value for an external, K=V (repeatable).\nPrefer --env-file: this goes into your shell history",
		"read an external's new values from KEY=VALUE lines\n(repeatable; later files win, --env wins over all files)")
	return cmd
}

func newResourceBackupsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "backups PROJECT/NAME",
		Short: "list its stored backups, newest last",
		Long: `Every stored backup of this resource. Postgres is dumped nightly and
kept fourteen days; the newest is the last line.

A backup is used by restoring it into a NEW resource — never over this
one. See ` + "`gg resource restore`" + `.`,
		Args: usageArgs(1, 1, "usage: gg resource backups PROJECT/NAME\n  e.g. gg resource backups shop/db"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdResourceBackups(args[0])
		},
	}
}

func newResourceBackupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "backup PROJECT/NAME",
		Short: "take a backup now, e.g. before something risky",
		Long: `Dump the resource to backup storage right now and print the key.

Safe to run any time: it reads the database and writes an object, nothing
else. Run it before a migration you are not sure about, and the nightly
schedule keeps running regardless. Kept fourteen days like every backup.`,
		Args: usageArgs(1, 1, "usage: gg resource backup PROJECT/NAME\n  e.g. gg resource backup shop/db"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdResourceBackup(args[0])
		},
	}
}

func newResourceRestoreCmd() *cobra.Command {
	var source, backupKey, size string
	var storage int
	cmd := &cobra.Command{
		Use:   "restore PROJECT/NEW-NAME",
		Short: "create a NEW resource from a backup",
		Long: `Bring backed-up data back, as a new resource.

This never overwrites anything: it provisions a fresh postgres under the
name you give, waits for it to run, and fills it from the backup. The
platform refuses to restore into a database that already holds data. So
this command needs no approval and is safe to reach for at three in the
morning.

The rest of the recovery, once the data is verified:
  1. gg deps add each dependent to the new resource, which also hands it
     the new resource's variables
  2. gg deps rm each dependent off the old one
  3. remove the old resource — the one step that destroys data, and the
     one that asks for human approval.

The variables are named after the resource, so they change with the
name: what read DB_URL now reads DB2_URL. A dependent that hard-codes
the old spelling in its own config needs a deploy as well.

Which backup: --source names the old resource and takes its newest dump
(the old resource may already be destroyed — that is fine, its backups
outlive it by fourteen days). --backup names an exact key from
` + "`gg resource backups`" + `.`,
		Args: usageArgs(1, 1, "usage: gg resource restore PROJECT/NEW-NAME --source OLD-NAME\n"+
			"  e.g. gg resource restore shop/db2 --source db"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdResourceRestore(args[0], source, backupKey, size, storage)
		},
	}
	cmd.Flags().StringVar(&source, "source", "",
		"the resource whose newest backup to restore (it may already be destroyed)")
	cmd.Flags().StringVar(&backupKey, "backup", "",
		"an exact backup `KEY`, from \"gg resource backups\"")
	cmd.Flags().StringVar(&size, "size", "",
		"the new resource's size: s, m or l (default s)")
	cmd.Flags().IntVar(&storage, "storage", 0,
		"the new resource's storage ceiling in GB (default 10)")
	return cmd
}

// --- domains ---------------------------------------------------------------

// newDomainCmd is its own verb rather than a flag on deploy, because a domain is
// a claim on a name rather than part of the artifact a deploy produces — and a
// flag could release one by being forgotten, which is the failure nobody sees.
func newDomainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "domain",
		Short: "put a service on the internet, at gagarin's name or your own",
		Long: `An address is declared, not deployed.

` + "`gg domain add PROJECT/SERVICE`" + ` gives a service the address gagarin
generates for it, and that is what makes it public. Add a name of your own after
it and the service answers on both.

A name you own is two steps, and only the first is gagarin's. Declaring it here
makes the service answer for that name; making the name resolve here is a record
you create at your registrar. Neither alone does anything, and nothing can be
issued over HTTPS until the record exists — Let's Encrypt has to reach the domain
to prove you control it. ` + "`gg status`" + ` says which of the two of you it is
waiting on.

A deploy never changes an address. Add and remove are the only two things that
do, and remove asks a human first — an address that stops answering breaks things
that are nowhere near this terminal.`,
	}
	cmd.AddCommand(newDomainAddCmd(), newDomainRemoveCmd(), newDomainListCmd())
	return cmd
}

func newDomainAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add PROJECT/SERVICE [DOMAIN]",
		Short: "give it an address on the internet",
		Args: usageArgs(1, 2, "usage: gg domain add PROJECT/SERVICE [DOMAIN]\n"+
			"  e.g. gg domain add shop/web                     an address from gagarin\n"+
			"       gg domain add shop/web shop.example.com    a name you own"),
		RunE: func(cmd *cobra.Command, args []string) error {
			// No domain is not a missing argument — it is the request for the
			// generated address, which is the common case and so is the short form.
			var domain string
			if len(args) == 2 {
				domain = args[1]
			}
			return cmdDomainAdd(args[0], domain)
		},
	}
}

func newDomainRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "rm PROJECT/SERVICE [DOMAIN]",
		Aliases: []string{"remove"},
		Short:   "take an address away (needs a human's approval)",
		Args: usageArgs(1, 2, "usage: gg domain rm PROJECT/SERVICE [DOMAIN]\n"+
			"  e.g. gg domain rm shop/web shop.example.com    release a name you own\n"+
			"       gg domain rm shop/web                     make it private again"),
		RunE: func(cmd *cobra.Command, args []string) error {
			var domain string
			if len(args) == 2 {
				domain = args[1]
			}
			return cmdDomainRemove(args[0], domain)
		},
	}
}

func newDomainListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ls PROJECT",
		Aliases: []string{"list"},
		Short:   "every address in the project, and who it is waiting on",
		Args:    usageArgs(1, 1, "usage: gg domain ls PROJECT"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdDomainList(args[0])
		},
	}
}

// --- registry --------------------------------------------------------------

func newRegistryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "registry",
		Short: "docker login, and copying images in from elsewhere",
	}
	cmd.AddCommand(newRegistryLoginCmd(), newRegistryCopyCmd())
	return cmd
}

func newRegistryLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "log docker in to the gagarin registry",
		Long: "log docker in to the gagarin registry, using the credential this\n" +
			"machine already holds. `gg auth` does this for you; this is here for\n" +
			"CI, and for when docker was installed after gagarin was.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdRegistryLogin()
		},
	}
}

func newRegistryCopyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "copy PROJECT/IMAGE[:TAG] SOURCE",
		Short: "copy an image from another registry into a project's space",
		Long: `Copy an image somebody else published into a project's own space, so
gagarin can run it.

  gg registry copy shop/postgres postgres:17-alpine

gagarin runs images only from its own registry, so this is how anything
you did not build gets in. Do not write a one-line Dockerfile that only
says FROM; that is the same thing, worse.

If a resource type exists for what you want, use that instead — see
` + "`gg resource add`" + `.`,
		Args: usageArgs(2, 2, "usage: gg registry copy PROJECT/IMAGE[:TAG] SOURCE\n"+
			"  e.g. gg registry copy shop/postgres postgres:17-alpine"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdRegistryCopy(args[0], args[1])
		},
	}
}

// --- the rest --------------------------------------------------------------

func newSkillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "the agent skill that ships inside this binary",
	}
	var dir string
	var agents []string
	var interactive bool
	install := &cobra.Command{
		Use:   "install",
		Short: "install the agent skill",
		Long: "install the agent skill so your agent knows how to use gagarin.\n\n" +
			"With no flags, installs for Claude Code — that has been the default\n" +
			"since before --agent existed, and stays that way. Name others with\n" +
			"--agent, repeated, comma-separated, or 'all' for every one gg knows,\n" +
			"or pick from a checklist with --interactive.\n" +
			"Known agents: " + strings.Join(agentKeys(), ", ") + ".",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if interactive {
				if dir != "" || cmd.Flags().Changed("agent") {
					return fmt.Errorf("--interactive cannot be combined with --dir or --agent")
				}
				picked, err := pickAgentsInteractively(cmd.OutOrStdout())
				if err != nil {
					return err
				}
				agents = picked
			}
			if dir != "" {
				if cmd.Flags().Changed("agent") {
					return fmt.Errorf("--dir installs to one explicit path; it cannot be combined with --agent")
				}
				return installSkill(dir)
			}
			if !cmd.Flags().Changed("agent") && !interactive {
				agents = []string{"claude"}
			}
			for _, key := range agents {
				if key == "all" {
					agents = agentKeys()
					break
				}
			}
			for _, key := range agents {
				if err := installSkillForAgent(key); err != nil {
					return err
				}
			}
			return nil
		},
	}
	install.Flags().StringVar(&dir, "dir", "",
		"install into this directory instead of the default location")
	install.Flags().StringSliceVar(&agents, "agent", nil,
		"agent(s) to install for, repeated or comma-separated (default: claude)")
	install.Flags().BoolVarP(&interactive, "interactive", "i", false,
		"choose agents from a checklist instead of --agent")
	show := &cobra.Command{
		Use:   "show",
		Short: "print the skill's contents",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdSkillShow()
		},
	}
	cmd.AddCommand(install, show)
	return cmd
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "which gg this is",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdVersion()
		},
	}
}
