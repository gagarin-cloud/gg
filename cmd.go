package main

// The command tree.
//
// This file only wires flags and positional arguments to the business logic
// that lives next to each concern (auth.go, registry.go, rollback.go, ...).
// Cobra owns dispatch, --help generation (wrapped to the terminal's width),
// and flag parsing; nothing here should contain a decision the API or the
// functions it calls could make instead.

import (
	"errors"
	"fmt"

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

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "gg",
		Short: "The gagarin CLI",
		Long: `gg is a thin wrapper over the gagarin API. It holds no state of its own
beyond an access token: anything gg can do, the API can do — there is no
second path.

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
		newDeployCmd(),
		newStatusCmd(),
		newLogsCmd(),
		newShareCmd(),
		newUnshareCmd(),
		newMembersCmd(),
		newDestroyCmd(),
		newHistoryCmd(),
		newRollbackCmd(),
		newEjectCmd(),
		newResourceCmd(),
		newRegistryCmd(),
		newSkillCmd(),
		newVersionCmd(),
		// The old spelling, kept working and kept out of the help. The published
		// agent skill in the wild still says it, and a skill is not something we
		// can update on somebody else's machine.
		newRegistryLoginAliasCmd(),
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
		Use:   "init [project]",
		Short: "create a project (defaults to directory name)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			return cmdInit(name)
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

func newDeployCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "build, push, and run the current directory",
		Args:  cobra.NoArgs,
	}
	v := bindDeployFlags(cmd.Flags())
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		f, err := v.finish()
		if err != nil {
			return err
		}
		return cmdDeploy(f)
	}
	return cmd
}

func newStatusCmd() *cobra.Command {
	var visual bool
	cmd := &cobra.Command{
		Use:   "status [project]",
		Short: "desired vs actual state for every service",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project := ""
			if len(args) > 0 {
				project = args[0]
			}
			return cmdStatus(project, visual)
		},
	}
	cmd.Flags().BoolVar(&visual, "visual", false,
		"draw it instead: opens a browser on a live dependency graph of the project")
	return cmd
}

func newLogsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logs SERVICE [project]",
		Short: "recent logs",
		Args:  usageArgs(1, 2, "usage: gg logs SERVICE [project]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			project := ""
			if len(args) > 1 {
				project = args[1]
			}
			return cmdLogs(args[0], project)
		},
	}
}

func newShareCmd() *cobra.Command {
	var role string
	cmd := &cobra.Command{
		Use:   "share EMAIL [project]",
		Short: "give somebody access to a project",
		Args:  usageArgs(1, 2, "usage: gg share EMAIL [project] [--role editor|viewer]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			project := ""
			if len(args) > 1 {
				project = args[1]
			}
			return cmdShare(args[0], project, role)
		},
	}
	cmd.Flags().StringVar(&role, "role", "editor",
		"editor deploys and manages but cannot delete the project; viewer reads")
	return cmd
}

func newUnshareCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unshare EMAIL [project]",
		Short: "take that access away",
		Args:  usageArgs(1, 2, "usage: gg unshare EMAIL [project]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			project := ""
			if len(args) > 1 {
				project = args[1]
			}
			return cmdUnshare(args[0], project)
		},
	}
}

func newMembersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "members [project]",
		Short: "who can reach a project, and as what",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project := ""
			if len(args) > 0 {
				project = args[0]
			}
			return cmdMembers(project)
		},
	}
}

func newDestroyCmd() *cobra.Command {
	var service, resource string
	cmd := &cobra.Command{
		Use:   "destroy [project]",
		Short: "delete the project and everything in it",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project := ""
			if len(args) > 0 {
				project = args[0]
			}
			if service != "" && resource != "" {
				return errors.New("pass --service or --resource, not both\n" +
					"  hint: `gg status` says which of the two a name is")
			}
			return cmdDestroy(project, service, resource)
		},
	}
	// One command destroys things, whatever they are. A second verb would be a
	// second place to audit and a second habit to have, and the approval flow
	// lives in the API's answer rather than here either way.
	cmd.Flags().StringVar(&service, "service", "",
		"delete just this one service instead. Refused while another service\nstill --needs it")
	cmd.Flags().StringVar(&resource, "resource", "",
		"delete just this one resource instead, and everything in it. Refused\nwhile a service still --needs it")
	return cmd
}

func newResourceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resource",
		Short: "databases and the like: what a project has, rather than what it runs",
	}
	cmd.AddCommand(newResourceAddCmd(), newResourceSecretsCmd())
	return cmd
}

func newResourceAddCmd() *cobra.Command {
	var project string
	var storage int
	cmd := &cobra.Command{
		Use:   "add TYPE NAME",
		Short: "provision a resource, e.g. `gg resource add postgres db`",
		Args: usageArgs(2, 2, "usage: gg resource add TYPE NAME\n"+
			"  the types that exist are: postgres"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdResourceAdd(args[0], args[1], project, storage)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project to add it to (default: directory name)")
	// The only knob, and it is honoured: it is the size of the volume. Anything
	// else about how a resource runs is the platform's decision.
	cmd.Flags().IntVar(&storage, "storage", 0, "how big its storage may get, in GB (default 10).\nSet once, at creation; it cannot be resized afterwards")
	return cmd
}

func newResourceSecretsCmd() *cobra.Command {
	var project, format string
	cmd := &cobra.Command{
		Use:   "secrets NAME",
		Short: "the credentials for connecting to a resource",
		Long: `Prints what a service needs to connect, and nothing else.

Nothing is injected anywhere: --needs opens the network path and grants no
environment, so these are passed to a deploy like any other variable.

  gg deploy --name api --needs db --env-file <(gg resource secrets db)`,
		Args: usageArgs(1, 1, "usage: gg resource secrets NAME"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdResourceSecrets(args[0], project, format)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project it belongs to (default: directory name)")
	cmd.Flags().StringVar(&format, "format", "env", "env (KEY=VALUE lines, for --env-file) or json")
	return cmd
}

func newHistoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "history SERVICE [project]",
		Short: "every deploy of a service, newest first",
		Args:  usageArgs(1, 2, "usage: gg history SERVICE [project]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			project := ""
			if len(args) > 1 {
				project = args[1]
			}
			return cmdHistory(args[0], project)
		},
	}
}

func newRollbackCmd() *cobra.Command {
	var to int
	cmd := &cobra.Command{
		Use:   "rollback SERVICE [project]",
		Short: "put the previous deploy back",
		Args:  usageArgs(1, 2, "usage: gg rollback SERVICE [project] [--to REVISION]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			project := ""
			if len(args) > 1 {
				project = args[1]
			}
			return cmdRollback(args[0], project, to)
		},
	}
	cmd.Flags().IntVar(&to, "to", 0,
		"a particular revision instead (see gg history). Refused across a\nchange of --volume")
	return cmd
}

func newEjectCmd() *cobra.Command {
	var outPath string
	cmd := &cobra.Command{
		Use:   "eject [project]",
		Short: "the Kubernetes manifests for this project. Owner only",
		Long:  "the Kubernetes manifests for this project, so you can run it\nsomewhere else. Owner only.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project := ""
			if len(args) > 0 {
				project = args[0]
			}
			return cmdEject(project, outPath)
		},
	}
	cmd.Flags().StringVarP(&outPath, "output", "o", "", "write to a file instead of stdout")
	return cmd
}

func newRegistryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "registry",
		Short: "docker login, and copying images into a project's space",
	}
	cmd.AddCommand(newRegistryLoginCmd(), newRegistryCopyCmd())
	return cmd
}

func newRegistryLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "log docker in to the gagarin registry",
		Long:  "log docker in to the gagarin registry, using the credential this\nmachine already holds.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdRegistryLogin()
		},
	}
}

// newRegistryLoginAliasCmd keeps `gg registry-login` working without showing it
// in --help.
func newRegistryLoginAliasCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "registry-login",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdRegistryLogin()
		},
	}
}

func newRegistryCopyCmd() *cobra.Command {
	var project, name string
	cmd := &cobra.Command{
		Use:   "copy IMAGE",
		Short: "copy a public image into this project's space",
		Long:  "copy a public image into this project's space, so gagarin can run\nit.",
		Args: func(cmd *cobra.Command, args []string) error {
			switch {
			case len(args) == 0:
				return errors.New("which image? e.g. gg registry copy postgres:17-alpine")
			case len(args) > 1:
				return fmt.Errorf("one image at a time; got %v", args)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdRegistryCopy(project, name, args[0])
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project to copy into (default: directory name)")
	cmd.Flags().StringVar(&name, "name", "", "repository to copy it to (default: the image's own name)")
	return cmd
}

func newSkillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "the agent skill that ships inside this binary",
	}
	cmd.AddCommand(newSkillInstallCmd(), newSkillShowCmd())
	return cmd
}

func newSkillInstallCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "install",
		Short: "install the agent skill (Claude Code)",
		Long:  "install the agent skill (Claude Code) so your agent knows how to\nuse gagarin.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return installSkill(dir)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "install into this directory instead of the default location")
	return cmd
}

func newSkillShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "print the skill's contents",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdSkillShow()
		},
	}
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
