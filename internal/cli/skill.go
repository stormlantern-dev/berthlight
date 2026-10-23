package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"stormlantern.nl/berthlight/internal/skill"
)

func cmdSkill(args []string, stdout, stderr io.Writer) error {
	usage := func() error {
		fmt.Fprintf(stderr, `usage: berthlight skill install   [--agent %s|all] [--project] [-C dir]
       berthlight skill uninstall [--agent ...] [--project] [-C dir]
       berthlight skill show

install writes berthlight's agent skill where coding agents load skills:
user-wide by default, or into the project with --project (commit it to
share it). Without --agent, for every agent found on this machine.
`, strings.Join(skill.IDs(), ","))
		return errUsage
	}
	if len(args) == 0 {
		return usage()
	}
	sub, args := args[0], args[1:]
	fs := newFlags("skill "+sub, stderr)
	agentList := fs.String("agent", "", "comma-separated agents, or all (default: those found on this machine)")
	project := fs.Bool("project", false, "install into the project instead of user-wide")
	dir := fs.String("C", ".", "project directory, with --project")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return usage()
	}
	switch sub {
	case "show":
		_, err := stdout.Write(skill.Content(currentVersion()))
		return err
	case "install", "uninstall":
	default:
		return usage()
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	var agents []skill.Agent
	if *agentList != "" {
		if agents, err = skill.Lookup(*agentList); err != nil {
			return err
		}
	} else if agents = skill.Detect(home); len(agents) == 0 {
		return fmt.Errorf("no coding agent found on this machine; name one with --agent %s or all", strings.Join(skill.IDs(), ","))
	}
	base := home
	if *project {
		if base, err = filepath.Abs(*dir); err != nil {
			return err
		}
	}
	targets := skill.Targets(agents, base, *project)

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
	if sub == "install" && *project && len(errs) == 0 {
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
