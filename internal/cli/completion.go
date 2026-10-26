package cli

import (
	_ "embed"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"stormlantern.nl/berthlight/internal/route"
	"stormlantern.nl/berthlight/internal/skill"
)

// nushellCompleter is `berthlight completion nushell`: an external
// completer that asks `berthlight __complete`, as the other shells'
// scripts do, and hands every other command to the completer set before.
//
//go:embed completion.nu
var nushellCompleter string

// shells are the shells completion writes scripts for, with what each
// subcommand's help says about installing one.
var shells = []struct {
	name, long, example string
	gen                 func(root *cobra.Command, w io.Writer) error
}{
	{
		name: "bash",
		long: `bash prints the completion script for bash. It needs the bash-completion
package. The deb, rpm and apk packages and Homebrew install it already;
otherwise put it where bash-completion finds it, or source it from
~/.bashrc.`,
		example: `  berthlight completion bash > ~/.local/share/bash-completion/completions/berthlight
  source <(berthlight completion bash)   # this shell only`,
		gen: func(root *cobra.Command, w io.Writer) error { return root.GenBashCompletionV2(w, true) },
	},
	{
		name: "zsh",
		long: `zsh prints the completion script for zsh. The deb, rpm and apk packages and
Homebrew install it already. Otherwise save it as _berthlight in a directory
on $fpath, before compinit runs in ~/.zshrc.`,
		example: `  berthlight completion zsh > "${fpath[1]}/_berthlight"
  source <(berthlight completion zsh)   # this shell only, after compinit`,
		gen: func(root *cobra.Command, w io.Writer) error { return root.GenZshCompletion(w) },
	},
	{
		name: "fish",
		long: `fish prints the completion script for fish. The deb, rpm and apk packages
and Homebrew install it already; otherwise save it in fish's completions
directory.`,
		example: `  berthlight completion fish > ~/.config/fish/completions/berthlight.fish
  berthlight completion fish | source   # this shell only`,
		gen: func(root *cobra.Command, w io.Writer) error { return root.GenFishCompletion(w, true) },
	},
	{
		name: "powershell",
		long: `powershell prints the completion script for PowerShell. Add it to your
profile to have it in every session.`,
		example: `  berthlight completion powershell | Out-String | Invoke-Expression   # this session only
  berthlight completion powershell >> $PROFILE`,
		gen: func(root *cobra.Command, w io.Writer) error { return root.GenPowerShellCompletionWithDesc(w) },
	},
	{
		name: "nushell",
		long: `nushell prints an external completer for nushell, which asks berthlight
itself what to complete. Every other command goes to the external completer
that was set before it, such as carapace's, so it combines with one.

Save it in an autoload directory, which nushell runs after config.nu, and
it is there in every new shell.`,
		example: `  berthlight completion nushell | save -f ($nu.user-autoload-dirs | first | path join berthlight.nu)`,
		gen: func(_ *cobra.Command, w io.Writer) error {
			_, err := io.WriteString(w, nushellCompleter)
			return err
		},
	},
}

func newCompletionCmd() *cobra.Command {
	var names []string
	for _, s := range shells {
		names = append(names, s.name)
	}
	cmd := &cobra.Command{
		Use:   "completion <shell>",
		Short: "Print shell completion for bash, zsh, fish, PowerShell or nushell",
		Long: `completion prints the script that lets your shell complete berthlight's
commands, flags and values: route names for rm, agents for skill, DNS
providers and CAs for cert, directories for -C. The deb, rpm and apk
packages and Homebrew install it for bash, zsh and fish.

See 'berthlight completion <shell> --help' for how to install it in each
shell.`,
		Example: `  berthlight completion fish > ~/.config/fish/completions/berthlight.fish
  berthlight completion nushell | save -f ($nu.user-autoload-dirs | first | path join berthlight.nu)`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return &usageError{msg: "completion needs a shell: " + strings.Join(names, ", "), usage: true}
			}
			return &usageError{msg: fmt.Sprintf("no completion for %q; berthlight has it for %s", args[0], strings.Join(names, ", ")), usage: true}
		},
	}
	for _, s := range shells {
		cmd.AddCommand(&cobra.Command{
			Use:               s.name,
			Short:             "Print the completion script for " + s.name,
			Long:              s.long,
			Example:           s.example,
			Args:              noArgs,
			ValidArgsFunction: cobra.NoFileCompletions,
			RunE: func(c *cobra.Command, args []string) error {
				return s.gen(c.Root(), c.OutOrStdout())
			},
		})
	}
	return cmd
}

func newDemoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "demo",
		Short: "Play the header animation",
		Long: `demo plays the header, the little demo scene that heads each command on a
terminal, from start to end: copper bars, the name dropping in, and a boat
that sails past an octopus to its berth. Ctrl-C stops it.

It is animated in terminals with the Kitty graphics protocol (Kitty,
Ghostty): Amiga-style, or the chunky C64 version on a slow link.
BERTHLIGHT_SCENE=amiga or =c64 picks one. Other terminals show its last
frame in half-block characters, or one line of text. It plays only on a
terminal, not in CI nor with BERTHLIGHT_NO_ANIMATION set.`,
		Example: `  berthlight demo
  BERTHLIGHT_SCENE=c64 berthlight demo`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		// On a terminal Run plays the scene before getting here.
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "berthlight demo plays on a terminal (not in CI, nor with BERTHLIGHT_NO_ANIMATION set).")
			return nil
		},
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Long: `version prints berthlight's version. The proxy image berthlight starts has
the same version, so a new berthlight starts a new proxy after
'berthlight down'.`,
		Example: `  berthlight version
  berthlight --version`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "berthlight", currentVersion())
			return nil
		},
	}
}

// completeRoutes completes the names of the routes, as ls lists them.
func completeRoutes(_ *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	c, err := LoadConfig()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	tab, err := route.Load(c.StateDir)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []cobra.Completion
	for _, r := range tab.Routes {
		if strings.HasPrefix(r.Name, toComplete) {
			out = append(out, cobra.CompletionWithDesc(r.Name, urlFor(c, r.Name)+" -> "+r.Addr()))
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeAgents completes skill's --agent, a comma-separated list.
func completeAgents(_ *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	prefix, word := "", toComplete
	if i := strings.LastIndex(toComplete, ","); i >= 0 {
		prefix, word = toComplete[:i+1], toComplete[i+1:]
	}
	chosen := map[string]bool{}
	for _, a := range strings.Split(prefix, ",") {
		chosen[strings.TrimSpace(a)] = true
	}
	var out []cobra.Completion
	for _, a := range skill.Agents {
		if !chosen[a.ID] && strings.HasPrefix(a.ID, word) {
			out = append(out, cobra.CompletionWithDesc(prefix+a.ID, a.Name))
		}
	}
	if prefix == "" && strings.HasPrefix("all", word) {
		out = append(out, cobra.CompletionWithDesc("all", "every agent"))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// fixedCompletions completes one of a fixed list of values.
func fixedCompletions(values []string) cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		var out []cobra.Completion
		for _, v := range values {
			if strings.HasPrefix(v, toComplete) {
				out = append(out, v)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeBases completes the base domains in BERTHLIGHT_BASE.
func completeBases(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	c, err := LoadConfig()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return fixedCompletions(c.Bases)(cmd, args, toComplete)
}

// completeOwnBases completes the base domains cert can get a certificate
// for: those in BERTHLIGHT_BASE other than localhost.
func completeOwnBases(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	c, err := LoadConfig()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var own []string
	for _, b := range c.Bases {
		if ownBase([]string{b}) != "" {
			own = append(own, b)
		}
	}
	return fixedCompletions(own)(cmd, args, toComplete)
}
