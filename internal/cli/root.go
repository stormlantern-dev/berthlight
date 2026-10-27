package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// errUsage makes a command exit 2 without another message: the command
// printed its own usage line.
var errUsage = errors.New("usage")

// usageError is a usage mistake: berthlight prints the message, and with
// usage set the command's usage under it, and exits 2.
type usageError struct {
	msg   string
	usage bool
}

func (e *usageError) Error() string { return e.msg }

// newRoot builds the command tree. Every command writes to the command's
// own output (cmd.OutOrStdout, cmd.ErrOrStderr), which Run sets.
func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "berthlight",
		Short: "Stable names for devcontainer dev servers, without published ports",
		Long: `berthlight gives each devcontainer's dev server a stable name, such as
http://shop.localhost:7480, instead of a published host port. One proxy,
bound to loopback, routes each name to its container over the route's own
container network.`,
		Example: `  berthlight devcontainer shop 8080   # wire a project's devcontainer.json once
  berthlight url                      # this worktree's URL
  berthlight status --watch           # every route, and what is wrong if anything is`,
		Version: currentVersion(),
		// Arguments that name no command land here, so the message and
		// exit code stay berthlight's own.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				rootUsage(cmd.ErrOrStderr())
				return errUsage
			}
			w := cmd.ErrOrStderr()
			fmt.Fprintf(w, "berthlight: unknown command %q\n", args[0])
			if s := cmd.SuggestionsFor(args[0]); len(s) > 0 {
				fmt.Fprintf(w, "Did you mean %s?\n", strings.Join(s, " or "))
			}
			fmt.Fprintln(w)
			rootUsage(w)
			return errUsage
		},
		ValidArgsFunction: cobra.NoFileCompletions,
		SilenceErrors:     true,
		SilenceUsage:      true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	root.SuggestionsMinimumDistance = 2
	root.SetVersionTemplate("berthlight {{.Version}}\n")
	root.PersistentFlags().Bool("no-header", false, "leave out the header (the little demo scene)")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &usageError{msg: err.Error(), usage: true}
	})
	defaultHelp := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if cmd == root {
			rootUsage(cmd.OutOrStdout())
			return
		}
		defaultHelp(cmd, args)
	})
	defaultUsage := root.UsageFunc()
	root.SetUsageFunc(func(cmd *cobra.Command) error {
		if cmd == root {
			rootUsage(cmd.OutOrStderr())
			return nil
		}
		return defaultUsage(cmd)
	})

	root.AddCommand(
		newAddCmd(), newRmCmd(), newLsCmd(), newStatusCmd(), newDevcontainerCmd(),
		newUpCmd(), newDownCmd(), newURLCmd(), newCertCmd(), newSkillCmd(),
		newDemoCmd(), newMCPCmd(), newVersionCmd(), newCompletionCmd(), newProxyCmd(),
	)
	root.InitDefaultHelpCmd()
	for _, c := range root.Commands() {
		if c.Name() == "help" {
			c.Short = "Show the overview, or a command's help"
			c.Long = `help without a command shows the overview of berthlight's commands and the
environment variables it reads. With a command, it shows what that command
does, its flags and examples, as <command> --help does.`
			c.Example = `  berthlight help
  berthlight help add
  berthlight skill install --help`
		}
	}
	return root
}

// noArgs refuses positional arguments as a usage error.
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return &usageError{msg: fmt.Sprintf("%s takes no arguments, got %q", cmd.CommandPath(), args[0]), usage: true}
	}
	return nil
}

// execute runs the command tree and turns its error into an exit code.
func execute(root *cobra.Command, stderr io.Writer) int {
	cmd, err := root.ExecuteC()
	if err == nil {
		return 0
	}
	var ue *usageError
	switch {
	case errors.Is(err, errUsage):
		return 2
	case errors.Is(err, errProblems):
		return 1
	case errors.As(err, &ue):
		fmt.Fprintln(stderr, "berthlight:", ue.msg)
		if ue.usage && cmd != nil {
			fmt.Fprintln(stderr)
			cmd.SetOut(stderr)
			cmd.SetErr(stderr)
			cmd.Usage()
		}
		return 2
	}
	fmt.Fprintln(stderr, "berthlight:", err)
	return 1
}

// normalizeArgs keeps the command lines Go's flag package accepted: a long
// flag with one dash (-json, -dry-run, -no-header) gets its second dash,
// and a one-letter flag with two (--C) loses one. Arguments after "--"
// stay as they are. While completing, the word being completed stays too.
func normalizeArgs(root *cobra.Command, args []string) []string {
	long, short := map[string]bool{"help": true, "version": true}, map[string]bool{"h": true}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, fs := range []*pflag.FlagSet{c.LocalFlags(), c.PersistentFlags()} {
			fs.VisitAll(func(f *pflag.Flag) {
				long[f.Name] = true
				if f.Shorthand != "" {
					short[f.Shorthand] = true
				}
			})
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)

	out := make([]string, len(args))
	copy(out, args)
	end := len(out)
	if len(out) > 0 && strings.HasPrefix(out[0], cobra.ShellCompRequestCmd) {
		end-- // the word being completed
	}
	for i := 0; i < end; i++ {
		a := out[i]
		if a == "--" {
			break
		}
		switch {
		case strings.HasPrefix(a, "--"):
			name, _, _ := strings.Cut(a[2:], "=")
			if len(name) == 1 && short[name] {
				out[i] = a[1:]
			}
		case strings.HasPrefix(a, "-") && len(a) > 2:
			name, _, _ := strings.Cut(a[1:], "=")
			if len(name) > 1 && long[name] {
				out[i] = "-" + a
			}
		}
	}
	return out
}
