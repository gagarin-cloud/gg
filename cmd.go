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
	cmd.Flags().BoolVar(&f.push, "push", false, "upload it afterwards, as `gg push` would")
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

A service is private unless you say otherwise. Private means reachable
inside its own project, by name, by the services that have declared they
need it — and from nowhere else. "--public" gives it an address on the
internet. Exposure is something you say out loud, because the two mistakes
do not cost the same: a service that should have been public fails the
first time somebody opens it, and a service that should have been private
does not fail at all.

A deploy changes the image and the environment, and nothing else. It
cannot set a domain, move a volume, or change what this service is allowed
to reach: those are declared by "gg domain", by the first deploy, and by
"gg deps", and none of them can be released by a deploy that forgets to
mention them.

Environment is replaced wholesale, because it is part of what this
revision ran with and is what a rollback puts back. Pass every variable
the service needs, every time.`,
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

As with "gg deploy", the service is private unless you pass "--public".`,
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
	var visual bool
	cmd := &cobra.Command{
		Use:   "status PROJECT",
		Short: "desired vs actual state for every service",
		Args: usageArgs(1, 1, "usage: gg status PROJECT\n"+
			"  gg projects lists the ones you can reach"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdStatus(args[0], visual)
		},
	}
	cmd.Flags().BoolVar(&visual, "visual", false,
		"draw it instead: opens a browser on a live dependency graph of the project")
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

This opens a route and grants nothing else. Credentials are passed as
environment on the deploy — see "gg resource secrets". Both halves are
required, and they fail differently: without the credentials you get an
authentication error, without the route you get a hang.

A deploy never changes any of this.`,
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
		Short: "let it reach these as well",
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
		Short:   "stop it reaching these",
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
	cmd.AddCommand(newResourceAddCmd(), newResourceSecretsCmd())
	return cmd
}

func newResourceAddCmd() *cobra.Command {
	var storage int
	cmd := &cobra.Command{
		Use:   "add PROJECT/NAME TYPE",
		Short: "provision a resource, e.g. `gg resource add shop/db postgres`",
		Long: `Provision something gagarin runs for you, rather than something you built.

  postgres   PostgreSQL 17, on a volume. Survives restarts.
  mongo      MongoDB 8, on a volume. Survives restarts.
  redis      An in-memory store. --storage is refused, and a restart
             loses everything in it: this is a cache, not a database.
             (It runs Valkey, the BSD-licensed fork; every redis client
             and every redis:// URL work unchanged.)

You name it and say how big. Every other decision is the platform's.

None of them is managed. One instance, one volume, no backups and no
failover — see ` + "`gg resource secrets`" + ` for how to connect, and the docs for
what that means before you put a client's data in one.`,
		Args: usageArgs(2, 2, "usage: gg resource add PROJECT/NAME TYPE\n"+
			"  e.g. gg resource add shop/db postgres\n"+
			"  the types that exist are: postgres, mongo, redis"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdResourceAdd(args[0], args[1], storage)
		},
	}
	// The only knob, and it is honoured rather than negotiated. Anything else
	// about how a resource runs is the platform's decision.
	cmd.Flags().IntVar(&storage, "storage", 0,
		"how big its storage may get, in GB (default 10).\nSet once, at creation; it cannot be resized afterwards.\nRefused for redis, which keeps nothing across a restart")
	return cmd
}

func newResourceSecretsCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "secrets PROJECT/NAME",
		Short: "the credentials for connecting to it",
		Long: `Print what a caller needs to connect, and nothing else.

Nothing is injected anywhere. Connecting is two steps and they do
different things:

  gg deploy shop/api:8080 api:v3 --env-file <(gg resource secrets shop/db)
  gg deps add shop/api db

The first supplies the credentials, the second opens the route. Without
the credentials you get an authentication error; without the route the
credentials are correct and the connection hangs.`,
		Args: usageArgs(1, 1, "usage: gg resource secrets PROJECT/NAME\n  e.g. gg resource secrets shop/db"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdResourceSecrets(args[0], format)
		},
	}
	cmd.Flags().StringVar(&format, "format", "env",
		"env (KEY=VALUE lines, for --env-file) or json")
	return cmd
}

// --- domains ---------------------------------------------------------------

// newDomainCmd is its own verb rather than a flag on deploy, because a domain is
// a claim on a name rather than part of the artifact a deploy produces — and a
// flag could release one by being forgotten, which is the failure nobody sees.
func newDomainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "domain",
		Short: "answer on a name you own, as well as the address gagarin gave you",
		Long: `A custom domain is declared, not deployed.

It is two steps, and only the first is gagarin's. Declaring it here makes the
service answer for that name; making the name resolve here is a record you
create at your registrar. Neither alone does anything, and nothing can be issued
over HTTPS until the record exists — Let's Encrypt has to reach the domain to
prove you control it.

` + "`gg status`" + ` says which of the two of you it is waiting on.

A deploy never changes a domain. Add and remove are the only two things that do.`,
	}
	cmd.AddCommand(newDomainAddCmd(), newDomainRemoveCmd(), newDomainListCmd())
	return cmd
}

func newDomainAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add PROJECT/SERVICE DOMAIN",
		Short: "declare a domain for a public service",
		Args: usageArgs(2, 2, "usage: gg domain add PROJECT/SERVICE DOMAIN\n"+
			"  e.g. gg domain add shop/web shop.example.com"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdDomainAdd(args[0], args[1])
		},
	}
}

func newDomainRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "rm PROJECT/SERVICE DOMAIN",
		Aliases: []string{"remove"},
		Short:   "release it",
		Args: usageArgs(2, 2, "usage: gg domain rm PROJECT/SERVICE DOMAIN\n"+
			"  e.g. gg domain rm shop/web shop.example.com"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdDomainRemove(args[0], args[1])
		},
	}
}

func newDomainListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ls PROJECT",
		Aliases: []string{"list"},
		Short:   "every declared domain, and who it is waiting on",
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
	install := &cobra.Command{
		Use:   "install",
		Short: "install the agent skill (Claude Code)",
		Long: "install the agent skill (Claude Code) so your agent knows how to\n" +
			"use gagarin.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return installSkill(dir)
		},
	}
	install.Flags().StringVar(&dir, "dir", "",
		"install into this directory instead of the default location")
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
