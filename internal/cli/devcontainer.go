package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"stormlantern.nl/berthlight/internal/devcontainer"
	"stormlantern.nl/berthlight/internal/diff"
	"stormlantern.nl/berthlight/internal/naming"
	"stormlantern.nl/berthlight/internal/tui"
)

type devcontainerOptions struct {
	file, dir string
	dryRun    bool
}

func newDevcontainerCmd() *cobra.Command {
	var o devcontainerOptions
	cmd := &cobra.Command{
		Use:   "devcontainer <project> <port>",
		Short: "Wire the project's devcontainer.json to berthlight",
		Long: `devcontainer edits the project's devcontainer.json so that its dev server,
on <port> inside the container, gets a berthlight name instead of a
published port. Run it once, in the main worktree, and commit the change.

The container joins its own network, bl-<folder name>, with that name as
its alias, and appPort entries for <port> go. An initializeCommand runs
'berthlight add' on the host before the container starts, so the route
exists by the time the dev server does. Each worktree gets its own name:
<project>.localhost for the main worktree, <project>-fix-login.localhost
for one in ../webshop-fix-login.

The file keeps its comments and layout; running devcontainer again changes
nothing. --dry-run prints the change as a diff instead. The dev server must
listen on 0.0.0.0 inside the container. Docker Compose devcontainers need
the network wiring by hand.`,
		Example: `  berthlight devcontainer shop 8080 --dry-run   # show the change
  berthlight devcontainer shop 8080             # make it
  berthlight devcontainer shop 5173 --file .devcontainer/web/devcontainer.json`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDevcontainer(o, args, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.file, "file", "", "devcontainer.json to edit (default: .devcontainer/devcontainer.json or .devcontainer.json)")
	f.StringVarP(&o.dir, "dir", "C", ".", "project directory")
	f.BoolVar(&o.dryRun, "dry-run", false, "print the changes as a diff instead of writing them")
	cmd.MarkFlagFilename("file", "json", "jsonc")
	cmd.MarkFlagDirname("dir")
	return cmd
}

func runDevcontainer(o devcontainerOptions, pos []string, stdout, stderr io.Writer) error {
	if len(pos) != 2 {
		fmt.Fprintln(stderr, "usage: berthlight devcontainer <project> <port> [--file devcontainer.json] [--dry-run]")
		return errUsage
	}
	project := pos[0]
	port, err := strconv.Atoi(pos[1])
	if err != nil {
		return fmt.Errorf("port %q is not a number", pos[1])
	}
	path := o.file
	if path == "" {
		if path, err = findDevcontainer(o.dir); err != nil {
			return err
		}
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, err := devcontainer.Edit(src, project, port)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if o.dryRun {
		d := diff.Unified("a/"+filepath.ToSlash(path), "b/"+filepath.ToSlash(path), string(src), string(out))
		if d == "" {
			fmt.Fprintf(stderr, "%s is already wired to berthlight\n", path)
		}
		fmt.Fprint(stdout, d)
		return nil
	}
	if string(out) == string(src) {
		fmt.Fprintf(stdout, "%s is already wired to berthlight\n", path)
	} else {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, out, info.Mode().Perm()); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "updated %s\n", path)
	}
	fmt.Fprintf(stdout, "Rebuild the container. The dev server must listen on 0.0.0.0:%d inside it.\n", port)
	c, cerr := LoadConfig()
	if label, lerr := naming.LabelFor(o.dir, project); cerr == nil && lerr == nil {
		u := urlFor(c, label)
		fmt.Fprintf(stdout, "This worktree's URL: %s\n", tui.NewTerm(stdout).Link(u, u))
	}
	worktreeAdvice(stdout, o.dir, devcontainer.Has(src, "workspaceMount"))
	return nil
}

// worktreeAdvice says what git inside a worktree's own devcontainer needs:
// worktrees with relative paths, and devcontainer up with
// --mount-git-worktree-common-dir, which a workspaceMount turns off
// (devcontainers/cli#1243).
func worktreeAdvice(w io.Writer, dir string, workspaceMount bool) {
	relative, absolute, err := naming.WorktreeGit(dir)
	if err != nil {
		return
	}
	if workspaceMount {
		fmt.Fprintln(w, "Note: this devcontainer.json sets workspaceMount, so git will not work inside other worktrees' containers (devcontainers/cli#1243).")
	}
	if len(absolute) > 0 {
		fmt.Fprintf(w, "Git cannot follow these worktrees from inside their containers, as their paths are absolute:\n  %s\nFix them with: git worktree repair --relative-paths\n", strings.Join(absolute, "\n  "))
	}
	if !relative {
		fmt.Fprintln(w, "For git inside a worktree's container: git config worktree.useRelativePaths true, and devcontainer up --mount-git-worktree-common-dir.")
	}
}

// findDevcontainer finds the project's devcontainer.json the way the
// devcontainer CLI does: .devcontainer/devcontainer.json, then
// .devcontainer.json, then one .devcontainer/<name>/devcontainer.json.
func findDevcontainer(dir string) (string, error) {
	for _, p := range []string{
		filepath.Join(dir, ".devcontainer", "devcontainer.json"),
		filepath.Join(dir, ".devcontainer.json"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	}
	found, _ := filepath.Glob(filepath.Join(dir, ".devcontainer", "*", "devcontainer.json"))
	switch len(found) {
	case 0:
		return "", fmt.Errorf("no devcontainer.json in %s; pass --file", dir)
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf("several devcontainer.json files; pick one with --file:\n  %s", strings.Join(found, "\n  "))
}
