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
	"stormlantern.nl/berthlight/internal/compose"
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

For a Docker Compose devcontainer (dockerComposeFile), Compose ignores
runArgs. Instead, devcontainer adds berthlight.compose.yml, next to
devcontainer.json, last in dockerComposeFile, and lists it in the
.gitignore there. The initializeCommand writes that override for its
worktree on each start: the service joins bl-<folder name> under that
alias, keeps its other networks, stops publishing <port> and, in a linked
worktree, gets the repository's git directory. Other services in the
stack get no name, and the proxy cannot reach them.

The file keeps its comments and layout; running devcontainer again changes
nothing. --dry-run prints the changes as a diff instead. The dev server
must listen on 0.0.0.0 inside the container.`,
		Example: `  berthlight devcontainer shop 8080 --dry-run   # show the change
  berthlight devcontainer shop 8080             # make it
  berthlight devcontainer shop 5173 --file .devcontainer/web/devcontainer.json
  berthlight devcontainer api 3000 --dry-run    # a Compose devcontainer: also shows the .gitignore`,
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
	rel, err := relDir(o.dir, path)
	if err != nil {
		return err
	}
	out, err := devcontainer.Edit(src, project, port, rel)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	isCompose := devcontainer.Has(src, "dockerComposeFile")
	edits := []fileEdit{{path: path, old: src, new: out, exists: true}}
	if isCompose {
		// The override is each worktree's own: git ignores it.
		gi := filepath.Join(filepath.Dir(path), ".gitignore")
		old, err := os.ReadFile(gi)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		edits = append(edits, fileEdit{path: gi, old: old, new: devcontainer.GitIgnore(old), exists: err == nil})
	}
	override := filepath.Join(filepath.Dir(path), compose.FileName)
	if o.dryRun {
		changed := false
		for _, e := range edits {
			a := "a/" + filepath.ToSlash(e.path)
			if !e.exists {
				a = "/dev/null"
			}
			d := diff.Unified(a, "b/"+filepath.ToSlash(e.path), string(e.old), string(e.new))
			changed = changed || d != ""
			fmt.Fprint(stdout, d)
		}
		if !changed {
			fmt.Fprintf(stderr, "%s is already wired to berthlight\n", path)
		}
		if isCompose {
			fmt.Fprintf(stderr, "On each start, the initializeCommand writes %s for its worktree.\n", override)
		}
		return nil
	}
	changed := false
	for _, e := range edits {
		if string(e.old) == string(e.new) {
			continue
		}
		mode := fs.FileMode(0o644)
		if info, err := os.Stat(e.path); err == nil {
			mode = info.Mode().Perm()
		}
		if err := os.WriteFile(e.path, e.new, mode); err != nil {
			return err
		}
		changed = true
		fmt.Fprintf(stdout, "updated %s\n", e.path)
	}
	if !changed {
		fmt.Fprintf(stdout, "%s is already wired to berthlight\n", path)
	}
	if isCompose {
		fmt.Fprintf(stdout, "On each start, the initializeCommand writes %s for its worktree; git ignores it.\n", override)
	}
	fmt.Fprintf(stdout, "Rebuild the container. The dev server must listen on 0.0.0.0:%d inside it.\n", port)
	c, cerr := LoadConfig()
	if label, lerr := naming.LabelFor(o.dir, project); cerr == nil && lerr == nil {
		u := urlFor(c, label)
		fmt.Fprintf(stdout, "This worktree's URL: %s\n", tui.NewTerm(stdout).Link(u, u))
	}
	if !isCompose { // the override mounts git for a Compose devcontainer
		worktreeAdvice(stdout, o.dir, devcontainer.Has(src, "workspaceMount"))
	}
	return nil
}

// fileEdit is one file devcontainer changes.
type fileEdit struct {
	path     string
	old, new []byte
	exists   bool
}

// relDir is the directory of devcontainer.json relative to the project
// directory, slash-separated: where the Compose override goes, as the
// initializeCommand names it.
func relDir(projectDir, file string) (string, error) {
	absDir, err := filepath.Abs(projectDir)
	if err != nil {
		return "", err
	}
	absFile, err := filepath.Abs(file)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absDir, filepath.Dir(absFile))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is not inside the project directory %s; pass -C", file, projectDir)
	}
	return filepath.ToSlash(rel), nil
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
