package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"stormlantern.nl/berthlight/internal/skill"
)

// skillUsage is skill's usage line, for a missing or unknown subcommand.
func skillUsage(stderr io.Writer) error {
	fmt.Fprintf(stderr, `usage: berthlight skill install   [--agent %s|all] [--project] [-C dir]
       berthlight skill uninstall [--agent ...] [--project] [-C dir]
       berthlight skill show

install writes berthlight's agent skill where coding agents load skills:
user-wide by default, or into the project with --project (commit it to
share it). Without --agent, for every agent found on this machine.
`, strings.Join(skill.IDs(), ","))
	return errUsage
}

type skillOptions struct {
	agents, dir string
	project     bool
}

func newSkillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Install the agent skill for Claude Code, Codex, OpenCode and pi",
		Long: `skill manages berthlight's agent skill: instructions that teach coding
agents to wire a project's devcontainer to berthlight, including the dev
server settings, docs and OAuth redirect URIs around it, and to find out
why a route does not answer.

install writes it where each agent loads skills: user-wide by default, or
into the project with --project, to commit for everyone working on it.
uninstall removes it again; show prints it.`,
		Example: `  berthlight skill install
  berthlight skill install --agent claude --project   # then commit .claude/skills
  berthlight skill show`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return skillUsage(cmd.ErrOrStderr())
		},
	}
	cmd.AddCommand(newSkillChangeCmd("install"), newSkillChangeCmd("uninstall"), newSkillShowCmd())
	return cmd
}

// skillArgs refuses arguments the way skill always has: with its usage.
func skillArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return skillUsage(cmd.ErrOrStderr())
	}
	return nil
}

func newSkillChangeCmd(sub string) *cobra.Command {
	var o skillOptions
	cmd := &cobra.Command{
		Use:               sub,
		Args:              skillArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkill(sub, o, cmd.OutOrStdout())
		},
	}
	if sub == "install" {
		cmd.Short = "Install the agent skill"
		cmd.Long = `install writes berthlight's agent skill, SKILL.md, where Claude Code, Codex,
OpenCode and pi load skills. User-wide by default, under your home
directory; with --project, into the project directory instead, to commit so
everyone working on the project gets it. Without --agent, for every agent
found on this machine.

Running it again updates the skill to this berthlight's version, and says
which copies were already current.`
		cmd.Example = `  berthlight skill install                             # every agent found here, user-wide
  berthlight skill install --agent claude,codex
  berthlight skill install --agent all --project -C ~/src/webshop`
	} else {
		cmd.Short = "Remove the agent skill"
		cmd.Long = `uninstall removes berthlight's agent skill from where install put it: for the
agents found on this machine, or those named with --agent; user-wide, or
from the project with --project. It says where it removed the skill and
where there was none.`
		cmd.Example = `  berthlight skill uninstall
  berthlight skill uninstall --agent all --project`
	}
	addSkillFlags(cmd, &o)
	return cmd
}

func addSkillFlags(cmd *cobra.Command, o *skillOptions) {
	f := cmd.Flags()
	f.StringVar(&o.agents, "agent", "", "comma-separated agents, or all (default: those found on this machine)")
	f.BoolVar(&o.project, "project", false, "install into the project instead of user-wide")
	f.StringVarP(&o.dir, "dir", "C", ".", "project directory, with --project")
	cmd.MarkFlagDirname("dir")
	cmd.RegisterFlagCompletionFunc("agent", completeAgents)
}

func newSkillShowCmd() *cobra.Command {
	var o skillOptions
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the agent skill",
		Long: `show prints berthlight's agent skill, the SKILL.md that install writes, for
this version of berthlight: to read it, or to put it somewhere install
does not know.`,
		Example: `  berthlight skill show
  berthlight skill show | save -f SKILL.md   # nushell`,
		Args:              skillArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := cmd.OutOrStdout().Write(skill.Content(currentVersion()))
			return err
		},
	}
	// show always accepted install's flags, and ignored them.
	addSkillFlags(cmd, &o)
	cmd.Flags().VisitAll(func(f *pflag.Flag) { f.Hidden = true })
	return cmd
}

func runSkill(sub string, o skillOptions, stdout io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	var agents []skill.Agent
	if o.agents != "" {
		if agents, err = skill.Lookup(o.agents); err != nil {
			return err
		}
	} else if agents = skill.Detect(home); len(agents) == 0 {
		return fmt.Errorf("no coding agent found on this machine; name one with --agent %s or all", strings.Join(skill.IDs(), ","))
	}
	base := home
	if o.project {
		if base, err = filepath.Abs(o.dir); err != nil {
			return err
		}
	}
	targets := skill.Targets(agents, base, o.project)

	var errs []error
	for _, t := range targets {
		where := displayPath(t.Dir, home)
		who := strings.Join(t.Agents, ", ")
		if sub == "uninstall" {
			removed, err := skill.Uninstall(t)
			switch {
			case err != nil:
				errs = append(errs, err)
			case removed:
				fmt.Fprintf(stdout, "removed %s (%s)\n", where, who)
			default:
				fmt.Fprintf(stdout, "not installed in %s (%s)\n", where, who)
			}
			continue
		}
		res, err := skill.Install(t, skill.Content(currentVersion()))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		fmt.Fprintf(stdout, "%s %s (%s)\n", res, filepath.Join(where, "SKILL.md"), who)
	}
	if sub == "install" && o.project && len(errs) == 0 {
		fmt.Fprintln(stdout, "Commit the skill so everyone working on the project gets it.")
	}
	return errors.Join(errs...)
}

// displayPath shows paths under home with ~.
func displayPath(p, home string) string {
	if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.Join("~", rel)
	}
	return p
}
